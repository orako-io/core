// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// CreateMachineTokenCommand mints a durable, non-interactive access token for
// a headless agent — org-admin only (phase 1). ProjectIDs, when non-empty,
// must be a subset of the caller's organization's projects; empty means
// unscoped (every project the token's owning member can reach), mirroring
// the OAuth-flow token convention (oauth.AuthCode.ProjectIDs).
type CreateMachineTokenCommand struct {
	// MemberID is the caller — the admin minting the token. The token is
	// attributed to them, exactly like an OAuth-flow token.
	MemberID uuid.UUID
	// OrgID is the caller's organization (CallerIdentity.OrgID), never taken
	// from the request — it scopes which projects ProjectIDs may name.
	OrgID uuid.UUID
	// IsOrgAdmin gates the whole command; resolved from org_members, never
	// taken from the request.
	IsOrgAdmin bool
	Label      string
	ProjectIDs []uuid.UUID `exhaustruct:"optional"`
}

// CreateMachineTokenResult is the freshly minted token: its metadata plus the
// raw secret, readable exactly once.
type CreateMachineTokenResult struct {
	ID         uuid.UUID
	Label      string
	ProjectIDs []uuid.UUID `exhaustruct:"optional"`
	CreatedAt  time.Time
	ExpiresAt  time.Time
	// Secret is the raw `mcp_at_…` bearer value. It is never persisted in the
	// clear and never returned again after this call.
	Secret string
}

// MintedMachineToken is the raw result of minting a machine token, returned
// by machineTokenMinter. It exists so this package never imports the oauth
// transport package's Token type — the infra adapter
// (infra/api/machine_token_service.go) translates between the two.
type MintedMachineToken struct {
	ID        uuid.UUID
	Secret    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// machineTokenMinter mints and persists a machine access token, scoped to
// projectIDs and stamped with label. Satisfied by a thin adapter over
// *oauth.Store (infra/api/machine_token_service.go) so this package never
// imports the oauth transport package directly.
type machineTokenMinter interface {
	MintMachineToken(ctx context.Context, memberID uuid.UUID, projectIDs []uuid.UUID, label string) (MintedMachineToken, error)
}

// machineTokenProjectReader resolves a project's owning organization, to
// confirm every requested project id belongs to the caller's organization
// before minting. *identity.ProjectStore satisfies it.
type machineTokenProjectReader interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Project, error)
}

// CreateMachineTokenHandler handles CreateMachineTokenCommand.
type CreateMachineTokenHandler struct {
	tokens   machineTokenMinter
	projects machineTokenProjectReader
}

// MustNewCreateMachineTokenHandler builds a handler. It panics on
// nil dependencies.
func MustNewCreateMachineTokenHandler(tokens machineTokenMinter, projects machineTokenProjectReader) CreateMachineTokenHandler {
	if tokens == nil {
		panic("CreateMachineTokenHandler requires a non-nil machineTokenMinter")
	}

	if projects == nil {
		panic("CreateMachineTokenHandler requires a non-nil machineTokenProjectReader")
	}

	return CreateMachineTokenHandler{tokens: tokens, projects: projects}
}

// Handle validates the caller is an org admin, the label is non-blank, and
// every requested project belongs to the caller's org, then mints the token.
func (h CreateMachineTokenHandler) Handle(ctx context.Context, cmd CreateMachineTokenCommand) (CreateMachineTokenResult, error) {
	if !cmd.IsOrgAdmin {
		return CreateMachineTokenResult{}, errs.ForbiddenError{Action: "create a machine token"}
	}

	if cmd.OrgID == uuid.Nil {
		return CreateMachineTokenResult{}, errs.InvalidError{Field: fieldOrgID, Reason: reasonNoOrgResolved}
	}

	label := strings.TrimSpace(cmd.Label)
	if label == "" {
		return CreateMachineTokenResult{}, errs.InvalidError{Field: "label", Reason: reasonEmpty}
	}

	if err := h.verifyProjectsInOrg(ctx, cmd.OrgID, cmd.ProjectIDs); err != nil {
		return CreateMachineTokenResult{}, err
	}

	minted, err := h.tokens.MintMachineToken(ctx, cmd.MemberID, cmd.ProjectIDs, label)
	if err != nil {
		return CreateMachineTokenResult{}, errs.InternalError{Err: fmt.Errorf("minting machine token: %w", err)}
	}

	return CreateMachineTokenResult{
		ID:         minted.ID,
		Label:      label,
		ProjectIDs: cmd.ProjectIDs,
		CreatedAt:  minted.CreatedAt,
		ExpiresAt:  minted.ExpiresAt,
		Secret:     minted.Secret,
	}, nil
}

// verifyProjectsInOrg confirms every id in ids belongs to orgID, closing the
// cross-tenant IDOR a bare IsOrgAdmin check would leave open (an admin of org
// A must not scope a machine token to a project of org B). An empty ids is a
// no-op — it means "unscoped", not "every project", so there is nothing to
// verify.
func (h CreateMachineTokenHandler) verifyProjectsInOrg(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) error {
	for _, id := range ids {
		project, err := h.projects.ByID(ctx, id)
		if err != nil {
			return translateErr(err, "project")
		}

		if project.OrgID != orgID {
			return errs.ForbiddenError{Action: "scope a machine token to a project outside your organization"}
		}
	}

	return nil
}
