// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// DeleteProjectCommand hard-deletes a project and every dependent row
// (project_members, conversations, messages, project_providers).
// This is irreversible, unlike SetProjectArchived's reversible freeze.
// Org-admin gated at the transport interceptor; OrgID additionally scopes the
// target to the caller's own organization (see verifyProjectInOrg). There is
// no "last project in the org" guard: no invariant in this codebase requires
// an org to retain a project (an org can legitimately end up with zero
// projects, e.g. mid-reorganization).
type DeleteProjectCommand struct {
	ProjectID uuid.UUID
	// OrgID is the caller's own organization (CallerIdentity.OrgID), never
	// taken from the request.
	OrgID uuid.UUID
}

// DeleteProjectResult is the (empty) result of DeleteProject.
type DeleteProjectResult struct{}

// projectDeleter is the narrow read+write port DeleteProject needs. Satisfied
// by *identity.ProjectStore; Delete cascades every dependent row via the
// projects FK's ON DELETE CASCADE (migrations 0001/0003).
type projectDeleter interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Project, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// DeleteProjectHandler handles DeleteProjectCommand.
type DeleteProjectHandler struct {
	store projectDeleter
}

// MustNewDeleteProjectHandler builds a handler. It panics on a nil
// dependency.
func MustNewDeleteProjectHandler(store projectDeleter) DeleteProjectHandler {
	if store == nil {
		panic("DeleteProjectHandler requires a non-nil projectDeleter")
	}

	return DeleteProjectHandler{store: store}
}

// Handle verifies the project belongs to the caller's org and hard-deletes it.
func (h DeleteProjectHandler) Handle(ctx context.Context, cmd DeleteProjectCommand) (DeleteProjectResult, error) {
	project, err := h.store.ByID(ctx, cmd.ProjectID)
	if err != nil {
		return DeleteProjectResult{}, translateErr(err, "project")
	}

	if err := verifyProjectInOrg(project, cmd.OrgID); err != nil {
		return DeleteProjectResult{}, err
	}

	if err := h.store.Delete(ctx, cmd.ProjectID); err != nil {
		return DeleteProjectResult{}, translateErr(err, "project")
	}

	return DeleteProjectResult{}, nil
}
