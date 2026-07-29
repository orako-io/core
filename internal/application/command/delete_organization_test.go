// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// fakeOrgDeleter is an in-memory orgDeleter for the DeleteOrganization tests.
type fakeOrgDeleter struct {
	orgs    map[uuid.UUID]model.Organization
	deleted map[uuid.UUID]bool
}

func newFakeOrgDeleter() *fakeOrgDeleter {
	return &fakeOrgDeleter{orgs: map[uuid.UUID]model.Organization{}, deleted: map[uuid.UUID]bool{}}
}

func (f *fakeOrgDeleter) ByID(_ context.Context, id uuid.UUID) (model.Organization, error) {
	org, ok := f.orgs[id]
	if !ok {
		return model.Organization{}, errs.NotFoundError{Resource: "organization"}
	}

	return org, nil
}

func (f *fakeOrgDeleter) DeleteOrganization(_ context.Context, id uuid.UUID) error {
	if _, ok := f.orgs[id]; !ok {
		return errs.NotFoundError{Resource: "organization"}
	}

	delete(f.orgs, id)
	f.deleted[id] = true

	return nil
}

// TestDeleteOrganization_HappyPath proves a delete with the matching typed name
// removes the org via the store.
func TestDeleteOrganization_HappyPath(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	repo := newFakeOrgDeleter()
	repo.orgs[orgID] = model.Organization{ID: orgID, Name: "Acme Corp"}

	h := MustNewDeleteOrganizationHandler(repo, fakeTransactor{})

	if _, err := h.Handle(t.Context(), DeleteOrganizationCommand{OrgID: orgID, ConfirmName: "Acme Corp"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if !repo.deleted[orgID] {
		t.Error("store.DeleteOrganization was not called")
	}
}

// TestDeleteOrganization_ConfirmNameCaseInsensitiveTrimmed proves the guard
// accepts a trimmed, differently-cased typed name.
func TestDeleteOrganization_ConfirmNameCaseInsensitiveTrimmed(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	repo := newFakeOrgDeleter()
	repo.orgs[orgID] = model.Organization{ID: orgID, Name: "Acme Corp"}

	h := MustNewDeleteOrganizationHandler(repo, fakeTransactor{})

	if _, err := h.Handle(t.Context(), DeleteOrganizationCommand{OrgID: orgID, ConfirmName: "  acme corp  "}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if !repo.deleted[orgID] {
		t.Error("store.DeleteOrganization was not called for a valid case-insensitive match")
	}
}

// TestDeleteOrganization_WrongConfirmNameRejected proves a mismatched typed
// name refuses the delete with InvalidError and never touches the store.
func TestDeleteOrganization_WrongConfirmNameRejected(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	repo := newFakeOrgDeleter()
	repo.orgs[orgID] = model.Organization{ID: orgID, Name: "Acme Corp"}

	h := MustNewDeleteOrganizationHandler(repo, fakeTransactor{})

	_, err := h.Handle(t.Context(), DeleteOrganizationCommand{OrgID: orgID, ConfirmName: "Wrong Name"})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("Handle(wrong name): got %v, want errs.InvalidError", err)
	}

	if invalid.Field != "confirm_name" {
		t.Errorf("InvalidError.Field = %q, want confirm_name", invalid.Field)
	}

	if repo.deleted[orgID] {
		t.Error("org must survive a mismatched confirm_name")
	}
}

// TestDeleteOrganization_NilOrgRejected proves a caller with no resolved org
// (identity carried no org) is refused before any store access.
func TestDeleteOrganization_NilOrgRejected(t *testing.T) {
	t.Parallel()

	repo := newFakeOrgDeleter()
	h := MustNewDeleteOrganizationHandler(repo, fakeTransactor{})

	_, err := h.Handle(t.Context(), DeleteOrganizationCommand{OrgID: uuid.Nil, ConfirmName: "anything"})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("Handle(nil org): got %v, want errs.InvalidError", err)
	}

	if invalid.Field != fieldOrgID {
		t.Errorf("InvalidError.Field = %q, want %q", invalid.Field, fieldOrgID)
	}
}

// TestDeleteOrganization_NotFound proves an absent org surfaces NotFoundError.
func TestDeleteOrganization_NotFound(t *testing.T) {
	t.Parallel()

	repo := newFakeOrgDeleter()
	h := MustNewDeleteOrganizationHandler(repo, fakeTransactor{})

	_, err := h.Handle(t.Context(), DeleteOrganizationCommand{OrgID: uuid.New(), ConfirmName: "Acme Corp"})

	var notFound errs.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Handle(absent org): got %v, want errs.NotFoundError", err)
	}
}
