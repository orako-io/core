// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// TestSetProjectArchived_ArchiveAndReactivate proves archive/reactivate flips
// the project's Archived() status both ways — the reversible-freeze round
// trip.
func TestSetProjectArchived_ArchiveAndReactivate(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	project, _ := model.NewProjectInOrg(uuid.New(), "Project", orgID)

	repo := newFakeProjectRepo()
	repo.projects[project.ID] = project

	h := MustNewSetProjectArchivedHandler(repo)

	if _, err := h.Handle(t.Context(), SetProjectArchivedCommand{ProjectID: project.ID, OrgID: orgID, Archived: true}); err != nil {
		t.Fatalf("Handle(archive): %v", err)
	}

	if !repo.projects[project.ID].Archived() {
		t.Fatal("after archive: Archived() = false, want true")
	}

	if _, err := h.Handle(t.Context(), SetProjectArchivedCommand{ProjectID: project.ID, OrgID: orgID, Archived: false}); err != nil {
		t.Fatalf("Handle(reactivate): %v", err)
	}

	if repo.projects[project.ID].Archived() {
		t.Error("after reactivate: Archived() = true, want false")
	}
}

// TestSetProjectArchived_CrossTenantRejected proves an org admin cannot
// archive a project belonging to a different organization.
func TestSetProjectArchived_CrossTenantRejected(t *testing.T) {
	t.Parallel()

	ownerOrg := uuid.New()
	callerOrg := uuid.New()
	project, _ := model.NewProjectInOrg(uuid.New(), "Foreign Project", ownerOrg)

	repo := newFakeProjectRepo()
	repo.projects[project.ID] = project

	h := MustNewSetProjectArchivedHandler(repo)

	_, err := h.Handle(t.Context(), SetProjectArchivedCommand{ProjectID: project.ID, OrgID: callerOrg, Archived: true})

	var forbidden errs.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("Handle(cross-tenant): got %v, want errs.ForbiddenError", err)
	}

	if repo.projects[project.ID].Archived() {
		t.Error("archived status must be unchanged on cross-tenant rejection")
	}
}

// TestSetProjectArchived_NotFound proves an absent project surfaces
// NotFoundError.
func TestSetProjectArchived_NotFound(t *testing.T) {
	t.Parallel()

	repo := newFakeProjectRepo()
	h := MustNewSetProjectArchivedHandler(repo)

	_, err := h.Handle(t.Context(), SetProjectArchivedCommand{ProjectID: uuid.New(), OrgID: uuid.New(), Archived: true})

	var notFound errs.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Handle(absent project): got %v, want errs.NotFoundError", err)
	}
}
