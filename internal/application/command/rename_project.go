// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// RenameProjectCommand renames a project. Org-admin gated at the transport
// interceptor; OrgID additionally scopes the target to the caller's own
// organization (see verifyProjectInOrg).
type RenameProjectCommand struct {
	ProjectID uuid.UUID
	// OrgID is the caller's own organization (CallerIdentity.OrgID), never
	// taken from the request.
	OrgID uuid.UUID
	Name  string
}

// RenameProjectResult is the (empty) result of RenameProject.
type RenameProjectResult struct{}

// projectRenamer is the narrow read+write port RenameProject needs. Satisfied
// by *identity.ProjectStore.
type projectRenamer interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Project, error)
	Rename(ctx context.Context, id uuid.UUID, name string) error
}

// RenameProjectHandler handles RenameProjectCommand.
type RenameProjectHandler struct {
	store projectRenamer
}

// MustNewRenameProjectHandler builds a handler. It panics on a nil
// dependency.
func MustNewRenameProjectHandler(store projectRenamer) RenameProjectHandler {
	if store == nil {
		panic("RenameProjectHandler requires a non-nil projectRenamer")
	}

	return RenameProjectHandler{store: store}
}

// Handle validates the new name, verifies the project belongs to the caller's
// org, and persists the rename.
func (h RenameProjectHandler) Handle(ctx context.Context, cmd RenameProjectCommand) (RenameProjectResult, error) {
	name := strings.TrimSpace(cmd.Name)
	if name == "" {
		return RenameProjectResult{}, errs.InvalidError{Field: "name", Reason: "must not be empty"}
	}

	project, err := h.store.ByID(ctx, cmd.ProjectID)
	if err != nil {
		return RenameProjectResult{}, translateErr(err, "project")
	}

	if err := verifyProjectInOrg(project, cmd.OrgID); err != nil {
		return RenameProjectResult{}, err
	}

	if err := h.store.Rename(ctx, cmd.ProjectID, name); err != nil {
		return RenameProjectResult{}, translateErr(err, "project")
	}

	return RenameProjectResult{}, nil
}
