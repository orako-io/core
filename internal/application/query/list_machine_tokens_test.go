// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// fakeMachineTokenLister is a machineTokenLister returning a fixed set of
// views and recording the last orgID it was asked about.
type fakeMachineTokenLister struct {
	views    []MachineTokenView
	err      error `exhaustruct:"optional"`
	calls    int
	gotOrgID uuid.UUID
}

func (f *fakeMachineTokenLister) ListMachineTokens(_ context.Context, orgID uuid.UUID) ([]MachineTokenView, error) {
	f.calls++
	f.gotOrgID = orgID

	return f.views, f.err
}

func TestListMachineTokens_AdminReads(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	now := time.Now()

	lister := &fakeMachineTokenLister{views: []MachineTokenView{
		{ID: uuid.New(), Label: "CI agent", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
	}}

	views, err := MustNewListMachineTokensHandler(lister).Handle(t.Context(), ListMachineTokensQuery{
		OrgID:      orgID,
		IsOrgAdmin: true,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(views) != 1 || views[0].Label != "CI agent" {
		t.Errorf("views = %+v, want one entry labeled CI agent", views)
	}

	if lister.gotOrgID != orgID {
		t.Errorf("lister called with orgID %s, want the caller's org %s", lister.gotOrgID, orgID)
	}
}

func TestListMachineTokens_NonAdminForbidden(t *testing.T) {
	t.Parallel()

	lister := &fakeMachineTokenLister{}

	_, err := MustNewListMachineTokensHandler(lister).Handle(t.Context(), ListMachineTokensQuery{
		OrgID:      uuid.New(),
		IsOrgAdmin: false,
	})

	var forbidden errs.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("want ForbiddenError, got %v", err)
	}

	if lister.calls != 0 {
		t.Error("a rejected query must never reach the store")
	}
}

func TestListMachineTokens_NoOrgResolvedInvalid(t *testing.T) {
	t.Parallel()

	lister := &fakeMachineTokenLister{}

	_, err := MustNewListMachineTokensHandler(lister).Handle(t.Context(), ListMachineTokensQuery{
		OrgID:      uuid.Nil,
		IsOrgAdmin: true,
	})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("want InvalidError, got %v", err)
	}

	if lister.calls != 0 {
		t.Error("an unresolved org must never reach the store")
	}
}
