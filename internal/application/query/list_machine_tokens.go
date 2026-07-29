// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// ListMachineTokensQuery reads every machine token minted in the caller's
// organization — org-admin only (phase 1), metadata only, never the secret or
// its hash. A machine token is org infrastructure, not a personal session: an
// admin sees every token in the org, not just the ones they personally
// minted.
type ListMachineTokensQuery struct {
	// OrgID is the caller's organization (CallerIdentity.OrgID), never taken
	// from the request — results are scoped to it.
	OrgID uuid.UUID
	// IsOrgAdmin gates the read; resolved from org_members, never taken from
	// the request.
	IsOrgAdmin bool
}

// MachineTokenView is the dashboard-facing metadata row for one minted
// machine token: label, project scope, and lifecycle timestamps — never the
// secret or its hash.
type MachineTokenView struct {
	ID    uuid.UUID
	Label string
	// ProjectIDs is the token's project scope (ordered, first = primary).
	// Empty means every project the owning member can reach in this org.
	ProjectIDs []uuid.UUID `exhaustruct:"optional"`
	CreatedAt  time.Time
	ExpiresAt  time.Time
	// LastUsedAt is unset until the token authenticates its first call.
	LastUsedAt *time.Time `exhaustruct:"optional"`
	// RevokedAt is unset while the token is live.
	RevokedAt *time.Time `exhaustruct:"optional"`
}

// machineTokenLister lists orgID's minted machine tokens. Satisfied by a thin
// adapter over *oauth.Store (infra/api/machine_token_service.go) so this
// package never imports the oauth transport package directly.
type machineTokenLister interface {
	ListMachineTokens(ctx context.Context, orgID uuid.UUID) ([]MachineTokenView, error)
}

// ListMachineTokensHandler handles ListMachineTokensQuery.
type ListMachineTokensHandler struct {
	tokens machineTokenLister
}

// MustNewListMachineTokensHandler builds a handler. It panics on a
// nil dependency.
func MustNewListMachineTokensHandler(tokens machineTokenLister) ListMachineTokensHandler {
	if tokens == nil {
		panic("ListMachineTokensHandler requires a non-nil machineTokenLister")
	}

	return ListMachineTokensHandler{tokens: tokens}
}

// Handle returns every machine token minted in the caller's org, newest
// first.
func (h ListMachineTokensHandler) Handle(ctx context.Context, q ListMachineTokensQuery) ([]MachineTokenView, error) {
	if !q.IsOrgAdmin {
		return nil, errs.ForbiddenError{Action: "list machine tokens"}
	}

	if q.OrgID == uuid.Nil {
		return nil, errs.InvalidError{Field: "org_id", Reason: "no organization resolved for the caller"}
	}

	tokens, err := h.tokens.ListMachineTokens(ctx, q.OrgID)
	if err != nil {
		return nil, translateReadError(err, "machine_tokens")
	}

	return tokens, nil
}
