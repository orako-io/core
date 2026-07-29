// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// SetProjectArchivedCommand freezes (Archived = true) or reactivates (false) a
// project. Archiving is a reversible freeze: data is kept, but the project
// drops out of reads, routing, and the seat count until reactivated.
// Org-admin gated at the transport interceptor; OrgID additionally scopes the
// target to the caller's own organization (see verifyProjectInOrg).
type SetProjectArchivedCommand struct {
	ProjectID uuid.UUID
	// OrgID is the caller's own organization (CallerIdentity.OrgID), never
	// taken from the request.
	OrgID    uuid.UUID
	Archived bool
}

// SetProjectArchivedResult is the (empty) result of SetProjectArchived.
type SetProjectArchivedResult struct{}

// projectArchiver is the narrow read+write port SetProjectArchived needs.
// Satisfied by *identity.ProjectStore.
type projectArchiver interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Project, error)
	SetArchived(ctx context.Context, id uuid.UUID, archived bool) error
}

// SetProjectArchivedHandler handles SetProjectArchivedCommand.
type SetProjectArchivedHandler struct {
	store projectArchiver
}

// MustNewSetProjectArchivedHandler builds a handler. It panics on a
// nil dependency.
func MustNewSetProjectArchivedHandler(store projectArchiver) SetProjectArchivedHandler {
	if store == nil {
		panic("SetProjectArchivedHandler requires a non-nil projectArchiver")
	}

	return SetProjectArchivedHandler{store: store}
}

// Handle verifies the project belongs to the caller's org and flips its
// archived status.
func (h SetProjectArchivedHandler) Handle(ctx context.Context, cmd SetProjectArchivedCommand) (SetProjectArchivedResult, error) {
	project, err := h.store.ByID(ctx, cmd.ProjectID)
	if err != nil {
		return SetProjectArchivedResult{}, translateErr(err, "project")
	}

	if err := verifyProjectInOrg(project, cmd.OrgID); err != nil {
		return SetProjectArchivedResult{}, err
	}

	if err := h.store.SetArchived(ctx, cmd.ProjectID, cmd.Archived); err != nil {
		return SetProjectArchivedResult{}, translateErr(err, "project")
	}

	return SetProjectArchivedResult{}, nil
}
