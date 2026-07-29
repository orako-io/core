// SPDX-License-Identifier: AGPL-3.0-or-later

// Package api — JWT Authenticator: the token proves *who*
// (subject/email); the caller's role is read from Orako's RBAC tables, never
// taken from the token.
package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/auth"
)

// accountResolver find-or-creates the authenticated human's account.
// *identity.AccountStore satisfies it.
type accountResolver interface {
	BySubject(ctx context.Context, subject string) (model.Account, error)
	ByEmail(ctx context.Context, email string) (model.Account, error)
	Create(ctx context.Context, account model.Account) error
}

// memberByEmailReader resolves a Orako member from a verified email and
// persists the account binding once the member's first login arrives.
// *identity.MemberStore satisfies it.
type memberByEmailReader interface {
	ByEmail(ctx context.Context, email string) (model.Member, error)
	Update(ctx context.Context, member model.Member) error
}

// memberByAccountReader resolves a member from an account id — the primary
// lookup: an org creator's member row is linked only by account_id (empty
// email), so it cannot be found by email.
type memberByAccountReader interface {
	ByAccountID(ctx context.Context, accountID uuid.UUID) (model.Member, error)
}

// memberByIDAuthReader resolves a member by id — used to attach an email or
// role to an identity a token has already resolved (never to authenticate).
// *identity.MemberStore satisfies it.
type memberByIDAuthReader interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Member, error)
}

// membershipReader lists the projects a member belongs to, with each project's
// owning org. *identity.ProjectStore satisfies it.
type membershipReader interface {
	ProjectsByMember(ctx context.Context, memberID uuid.UUID) ([]repository.ProjectWithRole, error)
}

// orgAdminReader is the authority for org-admin status, read from org_members
// and never from the token.
type orgAdminReader interface {
	RoleFor(ctx context.Context, orgID, accountID uuid.UUID) (model.OrgRole, error)
}

// PrincipalResolver maps verified token claims to an authenticated
// CallerIdentity using Orako's own identity and RBAC tables.
type PrincipalResolver interface {
	// Resolve is the member-gated path (dashboard/MCP): a dead
	// (non-CanAuthenticate) member is rejected with "member access has been
	// revoked".
	Resolve(ctx context.Context, claims auth.Claims) (CallerIdentity, error)
	// ResolveAccount is the member-optional path (POST /join and the OAuth
	// standalone join flow): it mirrors Resolve except a dead member is treated
	// as ABSENT (an account-only identity, MemberID nil) instead of an error, so
	// a re-joining human can start fresh. A live member still resolves fully.
	ResolveAccount(ctx context.Context, claims auth.Claims) (CallerIdentity, error)
}

// DBPrincipalResolver resolves the caller against Postgres: it find-or-creates
// the account (first-login provisioning), then resolves member and role from
// the membership tables. An account that has not yet joined a project resolves
// to an account-only identity (MemberID nil) that fails project RBAC.
type DBPrincipalResolver struct {
	accounts         accountResolver
	members          memberByEmailReader
	membersByAccount memberByAccountReader
	memberships      membershipReader
	orgAdmin         orgAdminReader
}

// NewDBPrincipalResolver builds a resolver over the given stores.
// membersByAccount is the primary member lookup; members (by email) is the
// fallback for invited-by-email members not yet linked to an account.
func NewDBPrincipalResolver(accounts accountResolver, members memberByEmailReader, membersByAccount memberByAccountReader, memberships membershipReader, orgAdmin orgAdminReader) DBPrincipalResolver {
	return DBPrincipalResolver{accounts: accounts, members: members, membersByAccount: membersByAccount, memberships: memberships, orgAdmin: orgAdmin}
}

// Resolve maps claims to a CallerIdentity, provisioning the account on first
// login. It returns an error (surfaced as Unauthenticated) only when the token
// carries no email or a store call fails. This is the member-gated path
// (dashboard/MCP): a dead member is rejected with "member access has been
// revoked".
func (r DBPrincipalResolver) Resolve(ctx context.Context, claims auth.Claims) (CallerIdentity, error) {
	return r.resolve(ctx, claims, false)
}

// ResolveAccount is the member-optional seam for POST /join: it resolves at
// least an account and tolerates a dead member by
// treating it as absent (MemberID stays nil) instead of failing, so a person
// whose old member was offboarded can redeem a fresh join code. A live member
// still resolves fully. It never returns "member access has been revoked".
func (r DBPrincipalResolver) ResolveAccount(ctx context.Context, claims auth.Claims) (CallerIdentity, error) {
	return r.resolve(ctx, claims, true)
}

// resolve is the shared body of Resolve/ResolveAccount. tolerateDeadMember
// selects the offboarding behavior: false (dashboard/MCP) hard-fails a dead
// member; true (/join) demotes it to an account-only identity.
func (r DBPrincipalResolver) resolve(ctx context.Context, claims auth.Claims, tolerateDeadMember bool) (CallerIdentity, error) {
	if claims.Email == "" {
		return CallerIdentity{}, errors.New("token carries no email; cannot establish identity")
	}

	account, err := r.findOrCreateAccount(ctx, claims)
	if err != nil {
		return CallerIdentity{}, err
	}

	identity := CallerIdentity{
		AccountID: account.ID,
		MemberID:  uuid.Nil,
		ProjectID: uuid.Nil,
		Role:      model.RoleUnspecified,
	}

	member, found, err := r.findMember(ctx, account.ID, claims.Email)
	if err != nil {
		return CallerIdentity{}, err
	}

	if !found {
		// Account-only identity: fresh account, no project yet.
		return identity, nil
	}

	// Offboarding boundary (H2): a removed/purged/deactivated/suspended member has
	// no access, even though the account (and, in local mode, its password) still
	// exists. On the member-gated path we deny here so soft-delete/deactivation
	// actually revokes the primary dashboard/JWT path, matching the MCP-token path.
	// On the member-optional /join path we instead demote to an account-only
	// identity so the redeem command can provision a fresh pending member — the
	// offboarded person "starts over" rather than inheriting the revoked hard-fail.
	if !member.Status.CanAuthenticate() {
		if tolerateDeadMember {
			return identity, nil
		}

		return CallerIdentity{}, errors.New("member access has been revoked")
	}

	if err := r.acceptInvitation(ctx, &member, account.ID); err != nil {
		return CallerIdentity{}, err
	}

	identity.MemberID = member.ID

	memberships, mErr := r.memberships.ProjectsByMember(ctx, member.ID)
	if mErr != nil {
		return CallerIdentity{}, fmt.Errorf("resolving memberships for %s: %w", member.ID, mErr)
	}

	// Dashboard sessions carry no token scope: the caller reaches every project
	// they belong to. Primary project = first membership; its owning org drives
	// the org-admin lookup. identity.Role stays unspecified (admin authority is
	// org-scoped).
	identity.ProjectIDs = projectIDsOf(memberships)

	if len(memberships) > 0 {
		identity.ProjectID = memberships[0].ID
		identity.OrgID = memberships[0].OrgID

		if err := r.resolveOrgAdmin(ctx, &identity, memberships[0].OrgID, account.ID); err != nil {
			return CallerIdentity{}, err
		}
	}

	return identity, nil
}

// resolveOrgAdmin sets identity.IsOrgAdmin from org_members. ErrNotFound means
// "not an admin", not an error; a nil orgID is a no-op.
func (r DBPrincipalResolver) resolveOrgAdmin(ctx context.Context, identity *CallerIdentity, orgID, accountID uuid.UUID) error {
	if orgID == uuid.Nil {
		return nil
	}

	role, err := r.orgAdmin.RoleFor(ctx, orgID, accountID)
	if err != nil {
		if errors.Is(err, adaptererr.ErrNotFound) {
			return nil
		}

		return fmt.Errorf("resolving org-admin status for account %s in org %s: %w", accountID, orgID, err)
	}

	identity.IsOrgAdmin = role == model.OrgRoleAdmin

	return nil
}

// acceptInvitation finalizes an email-invited member on their first
// authenticated login: it binds the account and activates the membership so
// the member leaves the "pending invitations" state. A member already bound to
// a DIFFERENT account is left untouched (never hijack an existing binding).
func (r DBPrincipalResolver) acceptInvitation(ctx context.Context, member *model.Member, accountID uuid.UUID) error {
	if member.AccountID != uuid.Nil && member.AccountID != accountID {
		return nil
	}

	bound := member.AccountID == accountID
	active := member.Status != model.MemberStatusInvited

	if bound && active {
		return nil
	}

	member.AccountID = accountID
	if !active {
		member.Status = model.MemberStatusActive
	}

	if err := r.members.Update(ctx, *member); err != nil {
		return fmt.Errorf("binding member %s to account %s: %w", member.ID, accountID, err)
	}

	return nil
}

// findMember resolves the caller's member row, preferring the account_id link
// and falling back to email. The bool is false when neither lookup finds a
// member — a legitimate account-only identity, not an error.
func (r DBPrincipalResolver) findMember(ctx context.Context, accountID uuid.UUID, email string) (model.Member, bool, error) {
	member, err := r.membersByAccount.ByAccountID(ctx, accountID)

	switch {
	case err == nil:
		return member, true, nil
	case errors.Is(err, adaptererr.ErrNotFound):
		// fall through to the email fallback
	default:
		return model.Member{}, false, fmt.Errorf("resolving member for account %s: %w", accountID, err)
	}

	member, err = r.members.ByEmail(ctx, email)

	// Zero value = account-only identity (no member yet); not an error.
	var none model.Member

	switch {
	case err == nil:
		return member, true, nil
	case errors.Is(err, adaptererr.ErrNotFound):
		return none, false, nil
	default:
		return model.Member{}, false, fmt.Errorf("resolving member for %s: %w", email, err)
	}
}

// findOrCreateAccount matches by external subject first (stable across email
// changes), then by email, then provisions a new account.
func (r DBPrincipalResolver) findOrCreateAccount(ctx context.Context, claims auth.Claims) (model.Account, error) {
	if claims.Subject != "" {
		if acc, err := r.accounts.BySubject(ctx, claims.Subject); err == nil {
			return acc, nil
		} else if !errors.Is(err, adaptererr.ErrNotFound) {
			return model.Account{}, fmt.Errorf("looking up account by subject: %w", err)
		}
	}

	if acc, err := r.accounts.ByEmail(ctx, claims.Email); err == nil {
		return acc, nil
	} else if !errors.Is(err, adaptererr.ErrNotFound) {
		return model.Account{}, fmt.Errorf("looking up account by email: %w", err)
	}

	account, err := model.NewAccount(uuid.New(), claims.Email, claims.Subject, "")
	if err != nil {
		return model.Account{}, err
	}

	if err := r.accounts.Create(ctx, account); err != nil {
		// Lost a race with a concurrent first login — re-fetch the winner.
		if errors.Is(err, adaptererr.ErrDuplicate) {
			return r.accounts.ByEmail(ctx, claims.Email)
		}

		return model.Account{}, fmt.Errorf("provisioning account for %s: %w", claims.Email, err)
	}

	return account, nil
}

// jwtAuthenticator verifies a Bearer JWT and resolves it to a CallerIdentity.
type jwtAuthenticator struct {
	verifier auth.TokenVerifier
	resolver PrincipalResolver
}

// NewJWTAuthenticator builds an Authenticator that verifies tokens with verifier
// and resolves identity with resolver.
func NewJWTAuthenticator(verifier auth.TokenVerifier, resolver PrincipalResolver) Authenticator {
	return jwtAuthenticator{verifier: verifier, resolver: resolver}
}

// Authenticate extracts the Bearer token, verifies it, and resolves the caller.
func (a jwtAuthenticator) Authenticate(ctx context.Context, header string) (CallerIdentity, error) {
	raw, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || raw == "" {
		return CallerIdentity{}, errors.New("missing or malformed Authorization header; expected: Bearer <jwt>")
	}

	claims, err := a.verifier.Verify(ctx, raw)
	if err != nil {
		return CallerIdentity{}, err
	}

	return a.resolver.Resolve(ctx, claims)
}

// AuthenticateAccount mirrors Authenticate but resolves via the member-optional
// seam: a verified token whose member has been offboarded yields an account-only
// identity (MemberID nil) instead of the "member access has been revoked" error,
// so the /join path can provision a fresh membership. A live member resolves
// fully, identical to Authenticate.
func (a jwtAuthenticator) AuthenticateAccount(ctx context.Context, header string) (CallerIdentity, error) {
	raw, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || raw == "" {
		return CallerIdentity{}, errors.New("missing or malformed Authorization header; expected: Bearer <jwt>")
	}

	claims, err := a.verifier.Verify(ctx, raw)
	if err != nil {
		return CallerIdentity{}, err
	}

	return a.resolver.ResolveAccount(ctx, claims)
}
