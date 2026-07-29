// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/errs"
)

// DeleteOrganizationCommand hard-deletes an organization and every row that
// belongs to it — projects, project_members, conversations, messages,
// candidates, kb entries, event_log, org_members, org_providers, org join
// tokens (all via ON DELETE CASCADE off organizations/projects) plus the
// members that belonged only to this org (deleted explicitly, since members
// carry no org FK). Irreversible.
//
// Org-admin gated at the transport interceptor. OrgID is the caller's own
// organization (CallerIdentity.OrgID), never taken from the request body.
// ConfirmName is the org name the admin typed; it must match the org's current
// name (trimmed, case-insensitive) or the delete is refused — a typed-name
// guard against accidental destruction.
type DeleteOrganizationCommand struct {
	OrgID       uuid.UUID
	ConfirmName string
}

// DeleteOrganizationResult is the (empty) result of DeleteOrganization.
type DeleteOrganizationResult struct{}

// orgDeleter is the narrow read+write port DeleteOrganization needs. ByID reads
// the org name for the confirm-name guard; DeleteOrganization performs the
// cascade. Both run inside the handler's transaction (each store call joins the
// ambient tx through its context). Satisfied by *identity.OrganizationStore.
type orgDeleter interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Organization, error)
	DeleteOrganization(ctx context.Context, id uuid.UUID) error
}

// DeleteOrganizationHandler handles DeleteOrganizationCommand.
type DeleteOrganizationHandler struct {
	store orgDeleter
	txor  service.Transactor
}

// MustNewDeleteOrganizationHandler builds a handler. It panics on a
// nil dependency.
func MustNewDeleteOrganizationHandler(store orgDeleter, txor service.Transactor) DeleteOrganizationHandler {
	if store == nil {
		panic("DeleteOrganizationHandler requires a non-nil orgDeleter")
	}

	if txor == nil {
		panic("DeleteOrganizationHandler requires a non-nil service.Transactor")
	}

	return DeleteOrganizationHandler{store: store, txor: txor}
}

// Handle validates the typed-name guard against the caller's own org, then
// hard-deletes the organization and all its data in one transaction. The org is
// the caller's own (OrgID from the auth identity), so there is no cross-tenant
// target to verify.
func (h DeleteOrganizationHandler) Handle(ctx context.Context, cmd DeleteOrganizationCommand) (DeleteOrganizationResult, error) {
	if cmd.OrgID == uuid.Nil {
		return DeleteOrganizationResult{}, errs.InvalidError{Field: fieldOrgID, Reason: reasonNoOrgResolved}
	}

	org, err := h.store.ByID(ctx, cmd.OrgID)
	if err != nil {
		return DeleteOrganizationResult{}, translateErr(err, "organization")
	}

	if !strings.EqualFold(strings.TrimSpace(cmd.ConfirmName), strings.TrimSpace(org.Name)) {
		return DeleteOrganizationResult{}, errs.InvalidError{
			Field:  "confirm_name",
			Reason: "must match the organization name",
		}
	}

	if err := h.txor.WithTx(ctx, func(ctx context.Context) error {
		return h.store.DeleteOrganization(ctx, cmd.OrgID)
	}); err != nil {
		return DeleteOrganizationResult{}, translateErr(err, "organization")
	}

	return DeleteOrganizationResult{}, nil
}
