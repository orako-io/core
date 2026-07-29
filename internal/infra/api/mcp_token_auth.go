// SPDX-License-Identifier: AGPL-3.0-or-later

// Package api — MCP-token Authenticator: opaque `mcp_at_…` bearers
// minted by Orako's thin OAuth Authorization Server (internal/infra/api/
// oauth). The bearer proves possession of an unrevoked, correctly-audienced
// token; identity and role come from the token's owning member and the RBAC
// tables, exactly like every other caller.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/infra/api/oauth"
)

// mcpTokenReader resolves and touches an oauth-issued token by its raw
// secret. *oauth.Store satisfies it.
type mcpTokenReader interface {
	GetToken(ctx context.Context, rawToken string, kind oauth.TokenKind) (oauth.Token, error)
	TouchToken(ctx context.Context, id uuid.UUID) error
}

// MCPTokenAuthenticator authenticates `mcp_at_…` bearers issued by Orako's
// thin OAuth AS and delegates every other Authorization header to fallback.
//
// resourceURL is this server's canonical MCP resource URL ({base}/mcp) — the
// confused-deputy guard the MCP Authorization spec requires: a token minted
// for a different resource, or a raw Supabase JWT that never went through
// this AS at all, must never be accepted. A production /mcp resource-server
// mount (phase 3) must wire this authenticator with a fallback that always
// rejects (never the dashboard's dev/jwt chain) — /mcp only
// accepts tokens this AS issued, never a dashboard session token, however
// valid that token is for the Connect-RPC surface. Wiring it into the
// Connect-RPC interceptor chain (main.go) additionally lets an mcp_at_ token
// call the dashboard API too, which is a harmless convenience, not a
// requirement /mcp depends on.
type MCPTokenAuthenticator struct {
	tokens      mcpTokenReader
	members     memberByIDAuthReader
	memberships membershipReader
	orgAdmin    orgAdminReader
	resourceURL string
	fallback    Authenticator
	logger      *slog.Logger
	now         func() time.Time
}

// NewMCPTokenAuthenticator wraps fallback with mcp-token support. resourceURL
// is the canonical MCP resource URL every accepted token's audience must
// exactly match.
func NewMCPTokenAuthenticator(
	tokens mcpTokenReader,
	members memberByIDAuthReader,
	memberships membershipReader,
	orgAdmin orgAdminReader,
	resourceURL string,
	fallback Authenticator,
	logger *slog.Logger,
) MCPTokenAuthenticator {
	return MCPTokenAuthenticator{
		tokens:      tokens,
		members:     members,
		memberships: memberships,
		orgAdmin:    orgAdmin,
		resourceURL: resourceURL,
		fallback:    fallback,
		logger:      logger,
		now:         time.Now,
	}
}

// Authenticate routes `mcp_at_…` bearers to the oauth token store and
// everything else to the wrapped authenticator. Unknown, revoked, expired, and
// wrong-audience tokens are all rejected with the same generic message:
// existence is never leaked.
func (a MCPTokenAuthenticator) Authenticate(ctx context.Context, header string) (CallerIdentity, error) {
	bearer, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || !oauth.IsAccessTokenSecret(bearer) {
		return a.fallback.Authenticate(ctx, header)
	}

	tok, err := a.tokens.GetToken(ctx, bearer, oauth.TokenKindAccess)
	if err != nil {
		if errors.Is(err, adaptererr.ErrNotFound) {
			return CallerIdentity{}, errors.New("unknown or revoked token")
		}

		return CallerIdentity{}, fmt.Errorf("resolving mcp token: %w", err)
	}

	if err := a.validateTokenState(tok); err != nil {
		return CallerIdentity{}, err
	}

	identity, err := a.resolveIdentity(ctx, tok.MemberID, tok.ProjectIDs)
	if err != nil {
		return CallerIdentity{}, err
	}

	// Best-effort usage stamp: an audit nicety must never fail the call.
	if err := a.tokens.TouchToken(ctx, tok.ID); err != nil {
		a.logger.WarnContext(ctx, "mcp token: stamping last_used_at failed", slog.Any("error", err))
	}

	return identity, nil
}

// AuthenticateAccount satisfies the member-optional seam. An mcp_at_ token is
// always minted for a live member, and the confused-deputy + offboarding guards
// must stay in force, so the token path here is identical to Authenticate (a
// dead member is still rejected). A non-mcp bearer is delegated to the wrapped
// authenticator's account-only path — that is where the /join tolerance lives.
func (a MCPTokenAuthenticator) AuthenticateAccount(ctx context.Context, header string) (CallerIdentity, error) {
	bearer, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || !oauth.IsAccessTokenSecret(bearer) {
		return a.fallback.AuthenticateAccount(ctx, header)
	}

	return a.Authenticate(ctx, header)
}

// validateTokenState rejects a revoked, expired, or wrong-audience token —
// the confused-deputy guard: a token minted for a different resource must
// never authenticate here, however otherwise valid it is.
func (a MCPTokenAuthenticator) validateTokenState(tok oauth.Token) error {
	if tok.Revoked() {
		return errors.New("unknown or revoked token")
	}

	if tok.ExpiredAt(a.now()) {
		return errors.New("token expired")
	}

	if tok.Resource != a.resourceURL {
		return fmt.Errorf("token audience %q does not match this resource %q", tok.Resource, a.resourceURL)
	}

	return nil
}

// resolveIdentity fetches the token's owning member and resolves
// CallerIdentity fresh from the RBAC tables — role and org-admin status are
// never taken from the token, exactly like every other authenticator.
func (a MCPTokenAuthenticator) resolveIdentity(ctx context.Context, memberID uuid.UUID, tokenScope []uuid.UUID) (CallerIdentity, error) {
	member, err := a.members.ByID(ctx, memberID)
	if err != nil {
		return CallerIdentity{}, fmt.Errorf("resolving mcp token member: %w", err)
	}

	if !member.Status.CanAuthenticate() {
		// removed/purged/deactivated/suspended — access revoked (H2).
		return CallerIdentity{}, errors.New("unknown or revoked token")
	}

	identity := CallerIdentity{
		AccountID: member.AccountID,
		MemberID:  member.ID,
		ProjectID: uuid.Nil,
		Role:      model.RoleUnspecified,
		Scoped:    len(tokenScope) > 0,
	}

	memberships, err := a.memberships.ProjectsByMember(ctx, member.ID)
	if err != nil {
		return CallerIdentity{}, fmt.Errorf("resolving mcp token memberships: %w", err)
	}

	// Restrict to the token's chosen scope (memberships ∩ token.project_ids);
	// the first survivor is the primary project.
	scoped := scopeProjects(memberships, tokenScope)

	if len(scoped) > 0 {
		identity.ProjectIDs = projectIDsOf(scoped)
		identity.ProjectID = scoped[0].ID
		identity.OrgID = scoped[0].OrgID

		if err := a.resolveOrgAdmin(ctx, &identity, scoped[0].OrgID, member.AccountID); err != nil {
			return CallerIdentity{}, err
		}
	}

	return identity, nil
}

// resolveOrgAdmin mirrors the JWT path: org-admin status is read from
// org_members, never inferred from the token.
func (a MCPTokenAuthenticator) resolveOrgAdmin(ctx context.Context, identity *CallerIdentity, orgID, accountID uuid.UUID) error {
	if accountID == uuid.Nil || orgID == uuid.Nil {
		return nil
	}

	role, err := a.orgAdmin.RoleFor(ctx, orgID, accountID)
	if err != nil {
		if errors.Is(err, adaptererr.ErrNotFound) {
			return nil
		}

		return fmt.Errorf("resolving org-admin status for mcp token: %w", err)
	}

	identity.IsOrgAdmin = role == model.OrgRoleAdmin

	return nil
}
