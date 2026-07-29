// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// fakeBaseAuthenticator stands in for the mode's upstream (dev/jwt)
// authenticator, before mcp-token wrapping.
type fakeBaseAuthenticator struct {
	identity CallerIdentity
	err      error
}

type fakeMemberByAccountInOrg struct {
	member       model.Member
	gotAccountID uuid.UUID
	gotOrgID     uuid.UUID
}

func (f *fakeMemberByAccountInOrg) ByAccountInOrg(_ context.Context, accountID, orgID uuid.UUID) (model.Member, error) {
	f.gotAccountID = accountID
	f.gotOrgID = orgID

	return f.member, nil
}

func (f fakeBaseAuthenticator) Authenticate(_ context.Context, _ string) (CallerIdentity, error) {
	return f.identity, f.err
}

// AuthenticateAccount is the member-optional seam; the fake returns the same
// identity so existing adapter tests exercise it unchanged, while the
// offboarded-member test can configure an account-only (MemberID nil) identity.
func (f fakeBaseAuthenticator) AuthenticateAccount(_ context.Context, _ string) (CallerIdentity, error) {
	return f.identity, f.err
}

func TestHumanAuthenticatorResolvesMemberAndEmail(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	base := fakeBaseAuthenticator{identity: CallerIdentity{MemberID: memberID}}
	members := &fakeMemberByID{member: model.Member{ID: memberID, Email: "responder@example.com"}}

	adapter := NewHumanAuthenticatorAdapter(base, members, nil, nil)

	identity, err := adapter.Authenticate(t.Context(), "some-session-token")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	if identity.MemberID != memberID {
		t.Errorf("MemberID = %s, want %s", identity.MemberID, memberID)
	}

	if identity.Email != "responder@example.com" {
		t.Errorf("Email = %q, want the member's email", identity.Email)
	}
}

func TestHumanAuthenticatorRejectsAccountOnly(t *testing.T) {
	t.Parallel()

	base := fakeBaseAuthenticator{identity: CallerIdentity{MemberID: uuid.Nil}}
	adapter := NewHumanAuthenticatorAdapter(base, &fakeMemberByID{}, nil, nil)

	if _, err := adapter.Authenticate(t.Context(), "some-session-token"); err == nil {
		t.Fatal("an account with no project membership must be rejected: no member to bind a token to")
	}
}

func TestHumanAuthenticatorPropagatesBaseFailure(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("invalid session")
	base := fakeBaseAuthenticator{err: wantErr}
	adapter := NewHumanAuthenticatorAdapter(base, &fakeMemberByID{}, nil, nil)

	_, err := adapter.Authenticate(t.Context(), "bad-token")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Authenticate error = %v, want %v", err, wantErr)
	}
}

func TestHumanAuthenticatorResolvesSelectedOrganizationMember(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	orgID := uuid.New()
	selectedMemberID := uuid.New()
	base := fakeBaseAuthenticator{identity: CallerIdentity{AccountID: accountID, MemberID: uuid.New()}}
	orgMembers := &fakeMemberByAccountInOrg{
		member: model.Member{
			ID:        selectedMemberID,
			AccountID: accountID,
			Email:     "selected@example.com",
			Status:    model.MemberStatusActive,
		},
	}
	adapter := NewHumanAuthenticatorAdapter(base, &fakeMemberByID{}, orgMembers, nil)

	identity, err := adapter.AuthenticateForOrg(t.Context(), "some-session-token", orgID)
	if err != nil {
		t.Fatalf("AuthenticateForOrg: %v", err)
	}
	if identity.MemberID != selectedMemberID {
		t.Errorf("MemberID = %s, want selected org member %s", identity.MemberID, selectedMemberID)
	}
	if orgMembers.gotAccountID != accountID || orgMembers.gotOrgID != orgID {
		t.Errorf("ByAccountInOrg called with (%s, %s), want (%s, %s)", orgMembers.gotAccountID, orgMembers.gotOrgID, accountID, orgID)
	}
}
