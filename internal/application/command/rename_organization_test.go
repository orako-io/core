// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// fakeOrgRenamer records the last Rename call so tests can assert the org id
// and (trimmed) name that reached the store.
type fakeOrgRenamer struct {
	id    uuid.UUID
	name  string
	calls int
	err   error `exhaustruct:"optional"`
}

func (f *fakeOrgRenamer) Rename(_ context.Context, id uuid.UUID, name string) error {
	f.id, f.name = id, name
	f.calls++

	return f.err
}

// TestRenameOrganization_Persists proves a valid rename reaches the store with
// the org id from the command (the caller identity) and a trimmed name, and
// echoes the saved name back.
func TestRenameOrganization_Persists(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	store := &fakeOrgRenamer{}
	h := MustNewRenameOrganizationHandler(store)

	res, err := h.Handle(t.Context(), RenameOrganizationCommand{OrgID: orgID, Name: "  New Org  "})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if store.id != orgID {
		t.Errorf("store org id: got %s, want %s (must come from identity)", store.id, orgID)
	}

	if store.name != "New Org" {
		t.Errorf("store name: got %q, want %q (trimmed)", store.name, "New Org")
	}

	if res.Name != "New Org" {
		t.Errorf("result name: got %q, want %q", res.Name, "New Org")
	}
}

// TestRenameOrganization_EmptyNameRejected proves a blank name is rejected
// before any store call.
func TestRenameOrganization_EmptyNameRejected(t *testing.T) {
	t.Parallel()

	store := &fakeOrgRenamer{}
	h := MustNewRenameOrganizationHandler(store)

	_, err := h.Handle(t.Context(), RenameOrganizationCommand{OrgID: uuid.New(), Name: "   "})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("Handle(blank name): got %v, want errs.InvalidError", err)
	}

	if store.calls != 0 {
		t.Error("a rejected command must not write")
	}
}

// TestRenameOrganization_NoOrgRejected proves a nil org id (no org resolved for
// the caller) is rejected.
func TestRenameOrganization_NoOrgRejected(t *testing.T) {
	t.Parallel()

	store := &fakeOrgRenamer{}
	h := MustNewRenameOrganizationHandler(store)

	_, err := h.Handle(t.Context(), RenameOrganizationCommand{OrgID: uuid.Nil, Name: "Acme"})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("Handle(nil org): got %v, want errs.InvalidError", err)
	}

	if store.calls != 0 {
		t.Error("a rejected command must not write")
	}
}

// TestRenameOrganization_TooLongRejected proves an over-long name is rejected.
func TestRenameOrganization_TooLongRejected(t *testing.T) {
	t.Parallel()

	store := &fakeOrgRenamer{}
	h := MustNewRenameOrganizationHandler(store)

	_, err := h.Handle(t.Context(), RenameOrganizationCommand{OrgID: uuid.New(), Name: strings.Repeat("a", maxOrgNameLen+1)})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("Handle(too long): got %v, want errs.InvalidError", err)
	}

	if store.calls != 0 {
		t.Error("a rejected command must not write")
	}
}
