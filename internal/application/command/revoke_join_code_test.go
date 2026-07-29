// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

type fakeJoinCodeRevoker struct {
	gotOrg uuid.UUID
	calls  int
	err    error
}

func (f *fakeJoinCodeRevoker) RevokeJoinToken(_ context.Context, orgID uuid.UUID) error {
	f.calls++
	f.gotOrg = orgID

	return f.err
}

func TestRevokeJoinCodeDelegatesToStore(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	revoker := &fakeJoinCodeRevoker{}

	h := MustNewRevokeJoinCodeHandler(revoker)

	if err := h.Handle(context.Background(), RevokeJoinCodeCommand{OrgID: orgID}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if revoker.calls != 1 || revoker.gotOrg != orgID {
		t.Errorf("revoke called %d time(s) for org %s, want 1 for %s", revoker.calls, revoker.gotOrg, orgID)
	}
}

func TestRevokeJoinCodeRejectsNilOrg(t *testing.T) {
	t.Parallel()

	revoker := &fakeJoinCodeRevoker{}
	h := MustNewRevokeJoinCodeHandler(revoker)

	err := h.Handle(context.Background(), RevokeJoinCodeCommand{OrgID: uuid.Nil})

	var inv errs.InvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("error = %v, want InvalidError for a nil org", err)
	}

	if revoker.calls != 0 {
		t.Error("a nil org must not reach the store")
	}
}
