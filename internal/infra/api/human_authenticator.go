// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/infra/api/oauth"
)

// accountByIDAuthReader resolves an account by id for the standalone `/join`
// flow, where the authenticated account may not have a member yet.
// *identity.AccountStore satisfies it.
type accountByIDAuthReader interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Account, error)
}

type memberByAccountInOrgAuthReader interface {
	ByAccountInOrg(ctx context.Context, accountID, orgID uuid.UUID) (model.Member, error)
}

// HumanAuthenticatorAdapter adapts an upstream Authenticator (the mode's
// dev/jwt authenticator — never the mcp-token-wrapped chain) into
// oauth.HumanAuthenticator, so the /authorize consent screen resolves the
// human through the exact same ORAKO_AUTH_MODE seam every other caller uses,
// never a second identity system.
type HumanAuthenticatorAdapter struct {
	base       Authenticator
	members    memberByIDAuthReader
	orgMembers memberByAccountInOrgAuthReader
	accounts   accountByIDAuthReader
}

// NewHumanAuthenticatorAdapter builds the adapter oauth.Server uses to verify
// the browser's proof of login at /authorize. accounts resolves identity for
// the separate member-optional `/join` flow.
func NewHumanAuthenticatorAdapter(base Authenticator, members memberByIDAuthReader, orgMembers memberByAccountInOrgAuthReader, accounts accountByIDAuthReader) HumanAuthenticatorAdapter {
	return HumanAuthenticatorAdapter{base: base, members: members, orgMembers: orgMembers, accounts: accounts}
}

// Authenticate verifies bearer via the wrapped upstream Authenticator and
// resolves the member it resolves to. An account with no project membership
// yet is rejected — there is no member row to bind an issued MCP token to.
func (a HumanAuthenticatorAdapter) Authenticate(ctx context.Context, bearer string) (oauth.MemberIdentity, error) {
	identity, err := a.base.Authenticate(ctx, "Bearer "+bearer)
	if err != nil {
		return oauth.MemberIdentity{}, err
	}

	if identity.MemberID == uuid.Nil {
		return oauth.MemberIdentity{}, errors.New("your account has not joined a project yet; join a project before connecting an agent")
	}

	// Email is cosmetic (shown on the consent screen) only; a lookup miss
	// here never fails the authorization for a member id the caller already
	// resolved successfully — degrade to an empty email instead.
	email := ""
	if member, lookupErr := a.members.ByID(ctx, identity.MemberID); lookupErr == nil {
		email = member.Email
	}

	return oauth.MemberIdentity{MemberID: identity.MemberID, Email: email}, nil
}

// AuthenticateForOrg verifies a bearer and resolves its membership in orgID.
func (a HumanAuthenticatorAdapter) AuthenticateForOrg(
	ctx context.Context,
	bearer string,
	orgID uuid.UUID,
) (oauth.MemberIdentity, error) {
	if a.orgMembers == nil {
		return oauth.MemberIdentity{}, errors.New("organization membership resolver is unavailable")
	}

	identity, err := a.base.AuthenticateAccount(ctx, "Bearer "+bearer)
	if err != nil {
		return oauth.MemberIdentity{}, err
	}

	if identity.AccountID == uuid.Nil {
		return oauth.MemberIdentity{}, errors.New("session has no account")
	}

	member, err := a.orgMembers.ByAccountInOrg(ctx, identity.AccountID, orgID)
	if err != nil {
		return oauth.MemberIdentity{}, err
	}

	if !member.Status.CanAuthenticate() {
		return oauth.MemberIdentity{}, errors.New("member access has been revoked")
	}

	return oauth.MemberIdentity{MemberID: member.ID, Email: member.Email}, nil
}

// AuthenticateAccount verifies bearer and resolves it to at least an account,
// tolerating an account with no member yet (MemberID stays nil) for `/join`.
// Email/DisplayName
// are best-effort (member first, then the account): a lookup miss degrades to an
// empty string, never fails the authorization.
func (a HumanAuthenticatorAdapter) AuthenticateAccount(ctx context.Context, bearer string) (oauth.AccountIdentity, error) {
	// Member-optional resolution: a verified token whose member was offboarded
	// resolves to an account-only identity (MemberID nil) rather than the
	// member-gated "revoked" hard-fail, so `/join` can provision a fresh
	// membership for a re-joining human. The member-required consent path
	// (Authenticate) still uses base.Authenticate.
	identity, err := a.base.AuthenticateAccount(ctx, "Bearer "+bearer)
	if err != nil {
		return oauth.AccountIdentity{}, err
	}

	out := oauth.AccountIdentity{AccountID: identity.AccountID, MemberID: identity.MemberID}

	if identity.MemberID != uuid.Nil {
		if member, lookupErr := a.members.ByID(ctx, identity.MemberID); lookupErr == nil {
			out.Email = member.Email
			out.DisplayName = member.DisplayName
		}
	}

	if out.Email == "" && a.accounts != nil {
		if account, lookupErr := a.accounts.ByID(ctx, identity.AccountID); lookupErr == nil {
			out.Email = account.Email

			if out.DisplayName == "" {
				out.DisplayName = account.DisplayName
			}
		}
	}

	return out, nil
}
