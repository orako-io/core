// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
)

// activeOrgHeader names the request header the dashboard sends to pick which of
// the caller's organizations is active for a request. It is honored only for
// orgs the caller participates in; identity (login) still comes from the token.
const activeOrgHeader = "Orako-Org-Id"

// ActiveOrgScoper re-scopes an authenticated identity to a requested org. It is
// the multi-org switch mechanism: the token fixes the login, the header picks
// the active org among those the caller belongs to.
type ActiveOrgScoper interface {
	// Scope returns the identity re-scoped to orgID with ok=true when the caller
	// participates in orgID; ok=false leaves the caller on their token org (the
	// header is ignored, never an error, so a stale header degrades gracefully).
	Scope(ctx context.Context, identity CallerIdentity, orgID uuid.UUID) (CallerIdentity, bool, error)
}

// orgProjectResolver resolves the caller's foothold in a target org: by
// account (the primary key of multi-org identity — each org has its OWN
// member row per account) or by member (dev-stub fallback, no account id).
type orgProjectResolver interface {
	ProjectInOrgForAccount(ctx context.Context, accountID, orgID uuid.UUID) (projectID, memberID uuid.UUID, ok bool, err error)
	ProjectInOrgForMember(ctx context.Context, memberID, orgID uuid.UUID) (uuid.UUID, bool, error)
}

// scopedMembershipReader re-resolves the switched-to member's project set, so
// RBAC under the new org sees THAT member's projects, not the token member's.
type scopedMembershipReader interface {
	ProjectsByMember(ctx context.Context, memberID uuid.UUID) ([]repository.ProjectWithRole, error)
}

// orgRoleReader resolves an account's role in an org (org-scoped RBAC).
type orgRoleReader interface {
	RoleFor(ctx context.Context, orgID, accountID uuid.UUID) (model.OrgRole, error)
}

// DBActiveOrgScoper is the DB-backed ActiveOrgScoper. Participation is proven by
// the member owning at least one project in the target org; admin authority is
// re-resolved from org_members for that org.
type DBActiveOrgScoper struct {
	projects    orgProjectResolver
	roles       orgRoleReader
	memberships scopedMembershipReader
}

// NewDBActiveOrgScoper builds a DBActiveOrgScoper.
func NewDBActiveOrgScoper(projects orgProjectResolver, roles orgRoleReader, memberships scopedMembershipReader) *DBActiveOrgScoper {
	return &DBActiveOrgScoper{projects: projects, roles: roles, memberships: memberships}
}

// Scope re-scopes identity to orgID when the caller has a foothold there. The
// lookup is ACCOUNT-keyed (each org has its own member row per account, so the
// token's member id says nothing about another org): the switched-to member id
// and its project set replace the token's, and admin authority is recomputed
// for the new org. The member-keyed path remains as the dev-stub fallback
// (no account id on the identity).
func (s *DBActiveOrgScoper) Scope(ctx context.Context, identity CallerIdentity, orgID uuid.UUID) (CallerIdentity, bool, error) {
	scoped := identity

	if identity.AccountID != uuid.Nil {
		projectID, memberID, ok, err := s.projects.ProjectInOrgForAccount(ctx, identity.AccountID, orgID)
		if err != nil || !ok {
			return identity, false, err
		}

		scoped.MemberID = memberID
		scoped.ProjectID = projectID
	} else {
		projectID, ok, err := s.projects.ProjectInOrgForMember(ctx, identity.MemberID, orgID)
		if err != nil || !ok {
			return identity, false, err
		}

		scoped.ProjectID = projectID
	}

	// The project set drives cross-project RBAC; it must be the switched-to
	// member's, never the token member's (whose projects live in another org).
	memberships, err := s.memberships.ProjectsByMember(ctx, scoped.MemberID)
	if err != nil {
		return identity, false, fmt.Errorf("resolving memberships for scoped member %s: %w", scoped.MemberID, err)
	}

	scoped.ProjectIDs = projectIDsOf(memberships)
	scoped.OrgID = orgID
	scoped.IsOrgAdmin = s.resolveAdmin(ctx, identity, orgID)

	return scoped, true, nil
}

// resolveAdmin recomputes admin authority for the target org. Real authority
// comes from org_members, keyed by account; the dev stub has no account id, so
// its token-provided flag is kept.
func (s *DBActiveOrgScoper) resolveAdmin(ctx context.Context, identity CallerIdentity, orgID uuid.UUID) bool {
	if identity.AccountID == uuid.Nil {
		return identity.IsOrgAdmin
	}

	role, err := s.roles.RoleFor(ctx, orgID, identity.AccountID)
	if err != nil {
		return false
	}

	return role == model.OrgRoleAdmin
}
