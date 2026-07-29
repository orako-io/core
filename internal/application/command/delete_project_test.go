// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// TestDeleteProject_RemovesProject proves a delete by the project's own org
// admin removes it from the store.
func TestDeleteProject_RemovesProject(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	project, _ := model.NewProjectInOrg(uuid.New(), "Project", orgID)

	repo := newFakeProjectRepo()
	repo.projects[project.ID] = project

	h := MustNewDeleteProjectHandler(repo)

	if _, err := h.Handle(t.Context(), DeleteProjectCommand{ProjectID: project.ID, OrgID: orgID}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if _, ok := repo.projects[project.ID]; ok {
		t.Error("project still present after delete")
	}

	if !repo.deleted[project.ID] {
		t.Error("store.Delete was not called for the project")
	}
}

// TestDeleteProject_CrossTenantRejected proves an org admin cannot delete a
// project belonging to a different organization.
func TestDeleteProject_CrossTenantRejected(t *testing.T) {
	t.Parallel()

	ownerOrg := uuid.New()
	callerOrg := uuid.New()
	project, _ := model.NewProjectInOrg(uuid.New(), "Foreign Project", ownerOrg)

	repo := newFakeProjectRepo()
	repo.projects[project.ID] = project

	h := MustNewDeleteProjectHandler(repo)

	_, err := h.Handle(t.Context(), DeleteProjectCommand{ProjectID: project.ID, OrgID: callerOrg})

	var forbidden errs.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("Handle(cross-tenant): got %v, want errs.ForbiddenError", err)
	}

	if _, ok := repo.projects[project.ID]; !ok {
		t.Error("project must survive a cross-tenant delete attempt")
	}
}

// TestDeleteProject_NotFound proves an absent project surfaces NotFoundError.
func TestDeleteProject_NotFound(t *testing.T) {
	t.Parallel()

	repo := newFakeProjectRepo()
	h := MustNewDeleteProjectHandler(repo)

	_, err := h.Handle(t.Context(), DeleteProjectCommand{ProjectID: uuid.New(), OrgID: uuid.New()})

	var notFound errs.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Handle(absent project): got %v, want errs.NotFoundError", err)
	}
}
