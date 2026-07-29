// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/pkg/errs"
)

// fakeMachineTokenRevoker is a machineTokenRevoker that records the last
// revoke call.
type fakeMachineTokenRevoker struct {
	err        error `exhaustruct:"optional"`
	calls      int
	gotOrgID   uuid.UUID
	gotTokenID uuid.UUID
}

func (f *fakeMachineTokenRevoker) RevokeMachineToken(_ context.Context, orgID, tokenID uuid.UUID) error {
	f.calls++
	f.gotOrgID = orgID
	f.gotTokenID = tokenID

	return f.err
}

func TestRevokeMachineToken_AdminRevokes(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	tokenID := uuid.New()

	revoker := &fakeMachineTokenRevoker{}

	err := MustNewRevokeMachineTokenHandler(revoker).Handle(t.Context(), RevokeMachineTokenCommand{
		OrgID:      orgID,
		IsOrgAdmin: true,
		TokenID:    tokenID,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if revoker.gotOrgID != orgID || revoker.gotTokenID != tokenID {
		t.Errorf("revoke called with (%s,%s), want (%s,%s)", revoker.gotOrgID, revoker.gotTokenID, orgID, tokenID)
	}
}

// TestRevokeMachineToken_AnyOrgAdminRevokes proves the command layer scopes
// by OrgID alone — a machine token is org infrastructure, so any admin call
// carrying the same OrgID reaches the store the same way, regardless of which
// member originally minted the token (the store itself is what a different
// integration test proves actually enforces this for real rows).
func TestRevokeMachineToken_AnyOrgAdminRevokes(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	tokenID := uuid.New()
	revoker := &fakeMachineTokenRevoker{}

	// A second admin of the SAME org revokes a token id they did not mint.
	err := MustNewRevokeMachineTokenHandler(revoker).Handle(t.Context(), RevokeMachineTokenCommand{
		OrgID:      orgID,
		IsOrgAdmin: true,
		TokenID:    tokenID,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if revoker.gotOrgID != orgID {
		t.Errorf("revoke scoped to org %s, want the caller's org %s", revoker.gotOrgID, orgID)
	}
}

func TestRevokeMachineToken_NonAdminForbidden(t *testing.T) {
	t.Parallel()

	revoker := &fakeMachineTokenRevoker{}

	err := MustNewRevokeMachineTokenHandler(revoker).Handle(t.Context(), RevokeMachineTokenCommand{
		OrgID:      uuid.New(),
		IsOrgAdmin: false,
		TokenID:    uuid.New(),
	})

	var forbidden errs.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("want ForbiddenError, got %v", err)
	}

	if revoker.calls != 0 {
		t.Error("a non-admin call must never reach the store")
	}
}

func TestRevokeMachineToken_NoOrgResolvedInvalid(t *testing.T) {
	t.Parallel()

	revoker := &fakeMachineTokenRevoker{}

	err := MustNewRevokeMachineTokenHandler(revoker).Handle(t.Context(), RevokeMachineTokenCommand{
		OrgID:      uuid.Nil,
		IsOrgAdmin: true,
		TokenID:    uuid.New(),
	})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("want InvalidError, got %v", err)
	}

	if revoker.calls != 0 {
		t.Error("an unresolved org must never reach the store")
	}
}

func TestRevokeMachineToken_NilIDInvalid(t *testing.T) {
	t.Parallel()

	revoker := &fakeMachineTokenRevoker{}

	err := MustNewRevokeMachineTokenHandler(revoker).Handle(t.Context(), RevokeMachineTokenCommand{
		OrgID:      uuid.New(),
		IsOrgAdmin: true,
		TokenID:    uuid.Nil,
	})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("want InvalidError, got %v", err)
	}
}

// TestRevokeMachineToken_NotFoundTranslated proves the store's
// adaptererr.ErrNotFound (unknown id, wrong org, or already revoked) maps to
// errs.NotFoundError rather than leaking the adapter sentinel.
func TestRevokeMachineToken_NotFoundTranslated(t *testing.T) {
	t.Parallel()

	revoker := &fakeMachineTokenRevoker{err: adaptererr.ErrNotFound}

	err := MustNewRevokeMachineTokenHandler(revoker).Handle(t.Context(), RevokeMachineTokenCommand{
		OrgID:      uuid.New(),
		IsOrgAdmin: true,
		TokenID:    uuid.New(),
	})

	var notFound errs.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("want NotFoundError, got %v", err)
	}
}
