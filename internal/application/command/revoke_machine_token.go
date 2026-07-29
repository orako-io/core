// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// RevokeMachineTokenCommand immediately invalidates a machine token belonging
// to the caller's organization — org-admin only (phase 1). A machine token is
// org infrastructure, not a personal session: ANY admin of the org that owns
// the token may revoke it, not only the admin who minted it (incident
// response and offboarding require this). Idempotent at the store level (an
// already-revoked token yields adaptererr.ErrNotFound, mapped below to
// errs.NotFoundError, not a distinct "already revoked" error).
type RevokeMachineTokenCommand struct {
	// OrgID is the caller's organization (CallerIdentity.OrgID), never taken
	// from the request — the store scopes the revoke to it so an admin can
	// never revoke a token belonging to a different org.
	OrgID uuid.UUID
	// IsOrgAdmin gates the command; resolved from org_members, never taken
	// from the request.
	IsOrgAdmin bool
	TokenID    uuid.UUID
}

// machineTokenRevoker revokes one of orgID's machine tokens by id. Satisfied
// directly by *oauth.Store.
type machineTokenRevoker interface {
	RevokeMachineToken(ctx context.Context, orgID, tokenID uuid.UUID) error
}

// RevokeMachineTokenHandler handles RevokeMachineTokenCommand.
type RevokeMachineTokenHandler struct {
	tokens machineTokenRevoker
}

// MustNewRevokeMachineTokenHandler builds a handler. It panics on a
// nil dependency.
func MustNewRevokeMachineTokenHandler(tokens machineTokenRevoker) RevokeMachineTokenHandler {
	if tokens == nil {
		panic("RevokeMachineTokenHandler requires a non-nil machineTokenRevoker")
	}

	return RevokeMachineTokenHandler{tokens: tokens}
}

// Handle revokes the caller org's machine token identified by cmd.TokenID.
func (h RevokeMachineTokenHandler) Handle(ctx context.Context, cmd RevokeMachineTokenCommand) error {
	if !cmd.IsOrgAdmin {
		return errs.ForbiddenError{Action: "revoke a machine token"}
	}

	if cmd.OrgID == uuid.Nil {
		return errs.InvalidError{Field: fieldOrgID, Reason: reasonNoOrgResolved}
	}

	if cmd.TokenID == uuid.Nil {
		return errs.InvalidError{Field: "id", Reason: reasonNilUUID}
	}

	if err := h.tokens.RevokeMachineToken(ctx, cmd.OrgID, cmd.TokenID); err != nil {
		return translateErr(err, "machine_token")
	}

	return nil
}
