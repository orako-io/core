// SPDX-License-Identifier: AGPL-3.0-or-later

// Package api — RBAC auth interceptor. Identity invariant (global
// invariant e): caller identity and role come from the auth token extracted
// here, never from request fields.
package api

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/orako-io/core/gen/orako/v1/orakov1connect"
	"github.com/orako-io/core/internal/application/domain/model"
)

// callerKeyType is the unexported context key type for CallerIdentity.
type callerKeyType struct{}

// callerKey is the singleton context key for CallerIdentity.
var callerKey = callerKeyType{} //nolint:gochecknoglobals // singleton context key; package-level by design

// CallerIdentity carries the authenticated caller's identity and role. It is
// injected into the context by the auth interceptor and must never be
// populated from request fields.
type CallerIdentity struct {
	// AccountID is the login identity; uuid.Nil for the dev stub.
	AccountID uuid.UUID `exhaustruct:"optional"`
	// MemberID is the caller as a project participant; uuid.Nil when the
	// account has not yet joined any project.
	MemberID uuid.UUID
	// ProjectID is the primary project the caller belongs to — the first of
	// ProjectIDs, used when a tool omits project_id.
	ProjectID uuid.UUID
	// ProjectIDs is the full set of projects this caller (this connection) may
	// reach, in priority order (first = primary). For a scoped token it is
	// memberships ∩ token scope; for the dashboard and unscoped tokens it is
	// every project the member belongs to. requireProjectMember checks
	// membership in this set.
	ProjectIDs []uuid.UUID `exhaustruct:"optional"`
	// Role is the caller's project role. No longer an authorization source
	// (admin authority is org-scoped); retained for the dev stub and display.
	Role model.Role
	// OrgID is the organization owning the caller's primary project.
	OrgID uuid.UUID `exhaustruct:"optional"`
	// IsOrgAdmin is the sole admin-authority signal, resolved from
	// org_members — never taken from the token.
	IsOrgAdmin bool `exhaustruct:"optional"`
	// Scoped is true when the connection's token restricted it to a chosen
	// subset of projects (a non-empty token scope). It caps reach at
	// ProjectIDs: the org-admin cross-project fallback in requireProjectMember
	// is suppressed for a scoped connection, so the scope is a hard ceiling
	// even for an org admin. Dashboard sessions and unscoped tokens are not
	// Scoped and keep the full org-admin reach.
	Scoped bool `exhaustruct:"optional"`
}

// CallerFromContext retrieves the CallerIdentity injected by the auth
// interceptor. The second return value is false when the context carries no
// identity (interceptor was bypassed or not registered).
func CallerFromContext(ctx context.Context) (CallerIdentity, bool) {
	v, ok := ctx.Value(callerKey).(CallerIdentity)
	return v, ok
}

// withCaller returns a new context with identity injected.
func withCaller(ctx context.Context, id CallerIdentity) context.Context {
	return context.WithValue(ctx, callerKey, id)
}

// adminProcedures is the set of Connect procedures that require org admin.
var adminProcedures = map[string]struct{}{ //nolint:gochecknoglobals // compile-time constant set; package-level by design
	orakov1connect.OrakoServiceCreateProjectProcedure:        {},
	orakov1connect.OrakoServiceRenameProjectProcedure:        {},
	orakov1connect.OrakoServiceSetProjectArchivedProcedure:   {},
	orakov1connect.OrakoServiceDeleteProjectProcedure:        {},
	orakov1connect.OrakoServiceDeleteConversationProcedure:   {},
	orakov1connect.OrakoServiceListProjectsDetailedProcedure: {},
	orakov1connect.OrakoServiceAddMemberProcedure:            {},
	orakov1connect.OrakoServiceInviteMembersProcedure:        {},
	orakov1connect.OrakoServiceAssignRoleProcedure:           {},
	orakov1connect.OrakoServiceRemoveMemberProcedure:         {},
	orakov1connect.OrakoServiceConfigureProviderProcedure:    {},
	orakov1connect.OrakoServiceSyncChatDirectoryProcedure:    {},
	orakov1connect.OrakoServiceDisconnectProviderProcedure:   {},
	orakov1connect.OrakoServiceSendProviderTestProcedure:     {},
	// ListMembers is deliberately NOT admin-gated: every org member may see the
	// team roster — you cannot collaborate with, or route a question to, a team
	// you cannot see. A non-admin gets the REDACTED roster (identity + expertise;
	// no email / external IDs / availability / admin flag — see the handler's
	// pbOrgMemberRoster). Mutating the roster (AddMember, RemoveMember,
	// AssignRole, SetOrgAdmin) and reading a single member's full detail
	// (GetOrgMember) stay admin-only below.
	orakov1connect.OrakoServiceGetOrgMemberProcedure:          {},
	orakov1connect.OrakoServiceSetMemberAvailabilityProcedure: {},
	orakov1connect.OrakoServiceSetMemberActivationProcedure:   {},
	orakov1connect.OrakoServiceSetOrgAdminProcedure:           {},
	// RenameOrganization mutates org identity — org-admin only.
	orakov1connect.OrakoServiceRenameOrganizationProcedure: {},
	// DeleteOrganization is the most destructive org op — org-admin only.
	orakov1connect.OrakoServiceDeleteOrganizationProcedure: {},
	// Join-code administration manages who may self-provision into the org.
	orakov1connect.OrakoServiceGenerateJoinCodeProcedure: {},
	orakov1connect.OrakoServiceGetJoinCodeProcedure:      {},
	orakov1connect.OrakoServiceRevokeJoinCodeProcedure:   {},
	// Machine tokens (phase 1) mint/list/revoke long-lived, non-interactive
	// credentials for a headless agent — org-admin only, the same trust
	// boundary as issuing any other durable credential.
	orakov1connect.OrakoServiceCreateMachineTokenProcedure: {},
	orakov1connect.OrakoServiceListMachineTokensProcedure:  {},
	orakov1connect.OrakoServiceRevokeMachineTokenProcedure: {},
	// Curated knowledge mutations are org-admin gated (authoring the team's
	// canonical answers). ListKnowledgeEntries is deliberately NOT here — every
	// member may read the curated answers, same as ListMembers.
	orakov1connect.OrakoServiceCreateKnowledgeEntryProcedure: {},
	orakov1connect.OrakoServiceUpdateKnowledgeEntryProcedure: {},
	orakov1connect.OrakoServiceMarkKnowledgeStaleProcedure:   {},
	// Phase 2 knowledge lifecycle: revalidation, the agent-suggested approval
	// queue, and promote-conversation are all curator actions — org-admin gated,
	// exactly like the create/edit/mark-stale mutations above. The suggest path
	// itself is MCP-only (any member) and never crosses this Connect surface.
	orakov1connect.OrakoServiceRevalidateKnowledgeEntryProcedure:       {},
	orakov1connect.OrakoServiceListPendingKnowledgeProcedure:           {},
	orakov1connect.OrakoServiceApproveKnowledgeEntryProcedure:          {},
	orakov1connect.OrakoServiceDismissKnowledgeEntryProcedure:          {},
	orakov1connect.OrakoServicePromoteConversationToKnowledgeProcedure: {},
}

// isAdminProcedure reports whether proc requires org-admin authority.
func isAdminProcedure(proc string) bool {
	_, ok := adminProcedures[proc]
	return ok
}

// Authenticator turns the raw Authorization header into an authenticated
// CallerIdentity, returning an error for missing/invalid credentials.
type Authenticator interface {
	// Authenticate is the member-gated path: an offboarded (non-CanAuthenticate)
	// member is rejected.
	Authenticate(ctx context.Context, authorizationHeader string) (CallerIdentity, error)
	// AuthenticateAccount is the member-optional path used by POST /join: it
	// resolves at least an account and tolerates an
	// offboarded member by leaving MemberID nil (never "member access has been
	// revoked"), so a re-joining human can provision a fresh membership.
	AuthenticateAccount(ctx context.Context, authorizationHeader string) (CallerIdentity, error)
}

// NewAuthInterceptor returns a Connect unary interceptor that:
//  1. Delegates identity extraction to the given Authenticator.
//  2. Re-scopes to the active org named in the Orako-Org-Id header (multi-org).
//  3. Injects the resulting CallerIdentity into the context.
//  4. Rejects unauthenticated requests with CodeUnauthenticated.
//  5. Rejects non-org-admin callers on admin RPCs with CodePermissionDenied.
//
// activeOrg may be nil to disable the org switch (single-org deployments).
func NewAuthInterceptor(auth Authenticator, activeOrg ActiveOrgScoper) connect.UnaryInterceptorFunc {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			header := req.Header().Get("Authorization")

			identity, err := auth.Authenticate(ctx, header)
			if err != nil {
				return nil, connect.NewError(connect.CodeUnauthenticated, err)
			}

			if activeOrg != nil {
				if identity, err = applyActiveOrg(ctx, activeOrg, identity, req.Header().Get(activeOrgHeader)); err != nil {
					return nil, err
				}
			}

			ctx = withCaller(ctx, identity)

			if isAdminProcedure(req.Spec().Procedure) {
				if !identity.IsOrgAdmin {
					return nil, connect.NewError(connect.CodePermissionDenied,
						errors.New("org admin required"))
				}
			}

			return next(ctx, req)
		})
	})
}

// applyActiveOrg re-scopes identity to the org named in the Orako-Org-Id header
// when the caller participates in it. Only an absent header or one naming the
// caller's current org leaves the identity unchanged. Every supplied invalid or
// inaccessible org fails closed so the request can never execute in the token's
// fallback org while the client believes another org is active.
func applyActiveOrg(ctx context.Context, scoper ActiveOrgScoper, identity CallerIdentity, raw string) (CallerIdentity, error) {
	if raw == "" {
		return identity, nil
	}

	orgID, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil || orgID == uuid.Nil {
		return identity, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid Orako-Org-Id header"))
	}

	if orgID == identity.OrgID {
		return identity, nil
	}

	scoped, ok, err := scoper.Scope(ctx, identity, orgID)
	if err != nil {
		return identity, connect.NewError(connect.CodeInternal, err)
	}

	if !ok {
		return identity, connect.NewError(connect.CodePermissionDenied, errors.New("organization is not accessible"))
	}

	return scoped, nil
}

// DevAuthenticator trusts an unsigned dev-stub Authorization header. Selected
// only when ORAKO_AUTH_MODE=dev; must never be used in a deployed environment.
type DevAuthenticator struct{}

// Authenticate parses the dev-stub token into a CallerIdentity.
func (DevAuthenticator) Authenticate(_ context.Context, header string) (CallerIdentity, error) {
	return parseDevToken(header)
}

// AuthenticateAccount resolves the dev-stub token the same way as Authenticate:
// a dev token carries an explicit member/project and never the reused-email dead
// member problem the /join seam guards against, so account-only tolerance is a
// no-op here.
func (DevAuthenticator) AuthenticateAccount(_ context.Context, header string) (CallerIdentity, error) {
	return parseDevToken(header)
}

// parseDevToken decodes the dev-stub Authorization header:
// "Bearer <memberID>:<projectID>:<role>[:orgadmin]". The optional 4th segment
// sets IsOrgAdmin; a 3-segment token with role "admin" is also org admin.
func parseDevToken(header string) (CallerIdentity, error) {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || token == "" {
		return CallerIdentity{}, errors.New("missing or malformed Authorization header; expected: Bearer <memberID>:<projectID>:<role>[:orgadmin]")
	}

	parts := strings.SplitN(token, ":", 5)
	if len(parts) < 3 {
		return CallerIdentity{}, errors.New("invalid dev token format; expected <memberID>:<projectID>:<role>[:orgadmin[:orgID]]")
	}

	memberID, err := uuid.Parse(parts[0])
	if err != nil {
		return CallerIdentity{}, errors.New("invalid member_id in token: " + err.Error())
	}

	projectID, err := uuid.Parse(parts[1])
	if err != nil {
		return CallerIdentity{}, errors.New("invalid project_id in token: " + err.Error())
	}

	role, err := parseRoleString(parts[2])
	if err != nil {
		return CallerIdentity{}, err
	}

	// Dev-only org-admin synthesis: an explicit 4th "orgadmin" segment, or the
	// legacy "admin" role. Real org-admin status is resolved from org_members.
	isOrgAdmin := role == model.RoleAdmin

	if len(parts) >= 4 {
		if parts[3] != "orgadmin" {
			return CallerIdentity{}, errors.New("invalid dev token flag; the 4th segment must be \"orgadmin\"")
		}

		isOrgAdmin = true
	}

	// Optional 5th segment: the org id, so dev tokens can exercise org-scoped
	// RPCs (create project, configure provider, billing) that derive OrgID from
	// the caller — the OIDC/local authenticators resolve it from the DB, but the
	// stateless dev stub cannot, so it is carried in the token instead.
	var orgID uuid.UUID

	if len(parts) == 5 {
		orgID, err = uuid.Parse(parts[4])
		if err != nil {
			return CallerIdentity{}, errors.New("invalid org_id in token: " + err.Error())
		}
	}

	return CallerIdentity{
		MemberID:   memberID,
		ProjectID:  projectID,
		ProjectIDs: []uuid.UUID{projectID},
		Role:       role,
		IsOrgAdmin: isOrgAdmin,
		OrgID:      orgID,
	}, nil
}

// parseRoleString converts a role string (dev/specialist/lead/admin) to
// model.Role. Returns an error for unrecognized values.
func parseRoleString(s string) (model.Role, error) {
	switch s {
	case "dev":
		return model.RoleDev, nil
	case "specialist":
		return model.RoleSpecialist, nil
	case "lead":
		return model.RoleLead, nil
	case "admin":
		return model.RoleAdmin, nil
	default:
		return model.RoleUnspecified, errors.New("unknown role in token: " + s + "; must be dev, specialist, lead, or admin")
	}
}
