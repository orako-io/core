// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// maxOrgNameLen caps an organization name's length (characters). Keeps the
// stored name to a sane, display-friendly size.
const maxOrgNameLen = 120

// RenameOrganizationCommand renames an organization. Org-admin gated at the
// transport interceptor; OrgID is the caller's own organization
// (CallerIdentity.OrgID), never taken from the request body.
type RenameOrganizationCommand struct {
	OrgID uuid.UUID
	Name  string
}

// RenameOrganizationResult carries the saved (trimmed) name.
type RenameOrganizationResult struct {
	Name string
}

// orgRenamer is the narrow write port RenameOrganization needs. Satisfied by
// *identity.OrganizationStore.
type orgRenamer interface {
	Rename(ctx context.Context, id uuid.UUID, name string) error
}

// RenameOrganizationHandler handles RenameOrganizationCommand.
type RenameOrganizationHandler struct {
	store orgRenamer
}

// MustNewRenameOrganizationHandler builds a handler. It panics on a
// nil dependency.
func MustNewRenameOrganizationHandler(store orgRenamer) RenameOrganizationHandler {
	if store == nil {
		panic("RenameOrganizationHandler requires a non-nil orgRenamer")
	}

	return RenameOrganizationHandler{store: store}
}

// Handle validates the new name and persists the rename. The org is the
// caller's own (OrgID from the auth identity), so there is no cross-tenant
// target to verify.
func (h RenameOrganizationHandler) Handle(ctx context.Context, cmd RenameOrganizationCommand) (RenameOrganizationResult, error) {
	if cmd.OrgID == uuid.Nil {
		return RenameOrganizationResult{}, errs.InvalidError{Field: fieldOrgID, Reason: reasonNoOrgResolved}
	}

	name := strings.TrimSpace(cmd.Name)
	if name == "" {
		return RenameOrganizationResult{}, errs.InvalidError{Field: "name", Reason: reasonEmpty}
	}

	if len(name) > maxOrgNameLen {
		return RenameOrganizationResult{}, errs.InvalidError{Field: "name", Reason: "must be 120 characters or fewer"}
	}

	if err := h.store.Rename(ctx, cmd.OrgID, name); err != nil {
		return RenameOrganizationResult{}, translateErr(err, "organization")
	}

	return RenameOrganizationResult{Name: name}, nil
}
