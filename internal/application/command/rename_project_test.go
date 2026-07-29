// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// TestRenameProject_Persists proves a rename by the project's own org admin
// persists the new name.
func TestRenameProject_Persists(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	project, _ := model.NewProjectInOrg(uuid.New(), "Old Name", orgID)

	repo := newFakeProjectRepo()
	repo.projects[project.ID] = project

	h := MustNewRenameProjectHandler(repo)

	if _, err := h.Handle(t.Context(), RenameProjectCommand{
		ProjectID: project.ID,
		OrgID:     orgID,
		Name:      "New Name",
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if got := repo.projects[project.ID].Name; got != "New Name" {
		t.Errorf("Name after rename: got %q, want %q", got, "New Name")
	}
}

// TestRenameProject_EmptyNameRejected proves a blank (or whitespace-only) name
// is rejected before any store call.
func TestRenameProject_EmptyNameRejected(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	project, _ := model.NewProjectInOrg(uuid.New(), "Old Name", orgID)

	repo := newFakeProjectRepo()
	repo.projects[project.ID] = project

	h := MustNewRenameProjectHandler(repo)

	_, err := h.Handle(t.Context(), RenameProjectCommand{ProjectID: project.ID, OrgID: orgID, Name: "   "})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("Handle(blank name): got %v, want errs.InvalidError", err)
	}

	if got := repo.projects[project.ID].Name; got != "Old Name" {
		t.Errorf("name must be unchanged on rejection: got %q", got)
	}
}

// TestRenameProject_CrossTenantRejected proves an org admin cannot rename a
// project belonging to a different organization (the cross-tenant IDOR
// verifyProjectInOrg closes).
func TestRenameProject_CrossTenantRejected(t *testing.T) {
	t.Parallel()

	ownerOrg := uuid.New()
	callerOrg := uuid.New()
	project, _ := model.NewProjectInOrg(uuid.New(), "Foreign Project", ownerOrg)

	repo := newFakeProjectRepo()
	repo.projects[project.ID] = project

	h := MustNewRenameProjectHandler(repo)

	_, err := h.Handle(t.Context(), RenameProjectCommand{ProjectID: project.ID, OrgID: callerOrg, Name: "Hijacked"})

	var forbidden errs.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("Handle(cross-tenant): got %v, want errs.ForbiddenError", err)
	}

	if got := repo.projects[project.ID].Name; got != "Foreign Project" {
		t.Errorf("name must be unchanged on cross-tenant rejection: got %q", got)
	}
}

// TestRenameProject_NotFound proves an absent project surfaces NotFoundError.
func TestRenameProject_NotFound(t *testing.T) {
	t.Parallel()

	repo := newFakeProjectRepo()
	h := MustNewRenameProjectHandler(repo)

	_, err := h.Handle(t.Context(), RenameProjectCommand{ProjectID: uuid.New(), OrgID: uuid.New(), Name: "x"})

	var notFound errs.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Handle(absent project): got %v, want errs.NotFoundError", err)
	}
}
