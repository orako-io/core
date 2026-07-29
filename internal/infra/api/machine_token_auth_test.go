// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/infra/api/oauth"
)

// TestMCPTokenAuthenticatesMachineToken proves a machine token (phase 1) —
// kind=access, ClientID = oauth.MachineClientID, no paired refresh row —
// authenticates through MCPTokenAuthenticator exactly like an OAuth-flow
// access token: same store shape, same validation, no special-casing needed.
func TestMCPTokenAuthenticatesMachineToken(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	accountID := uuid.New()
	projectID := uuid.New()
	orgID := uuid.New()
	raw := "mcp_at_" + uuid.NewString()

	tokens := &fakeMCPTokenStore{
		tokens: map[string]oauth.Token{
			raw: {
				ID:        uuid.New(),
				MemberID:  memberID,
				ClientID:  oauth.MachineClientID,
				Resource:  testMCPResourceURL,
				Kind:      oauth.TokenKindAccess,
				GrantID:   uuid.New(),
				ExpiresAt: time.Now().Add(oauth.MachineTokenTTL),
			},
		},
	}

	authn := NewMCPTokenAuthenticator(
		tokens,
		&fakeMemberByID{member: model.Member{ID: memberID, AccountID: accountID, Status: model.MemberStatusActive}},
		fakeMemberships{rows: []repository.ProjectWithRole{{ID: projectID, OrgID: orgID}}},
		fakeOrgAdmin{role: model.OrgRoleAdmin},
		testMCPResourceURL,
		&fakeFallback{},
		slog.New(slog.DiscardHandler),
	)

	identity, err := authn.Authenticate(t.Context(), "Bearer "+raw)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	if identity.MemberID != memberID {
		t.Errorf("identity member = %s, want %s", identity.MemberID, memberID)
	}

	if len(tokens.touched) != 1 {
		t.Error("last_used_at not stamped")
	}
}

// TestMCPTokenRevokedMachineTokenRejected proves revoking a machine token
// (RevokedAt set, exactly as Store.RevokeMachineToken does) makes the next
// MCPTokenAuthenticator call reject it — the dashboard's "revoke" action
// takes effect on the very next agent call.
func TestMCPTokenRevokedMachineTokenRejected(t *testing.T) {
	t.Parallel()

	raw := "mcp_at_" + uuid.NewString()
	revokedAt := time.Now()

	tokens := &fakeMCPTokenStore{
		tokens: map[string]oauth.Token{
			raw: {
				ID:        uuid.New(),
				MemberID:  uuid.New(),
				ClientID:  oauth.MachineClientID,
				Resource:  testMCPResourceURL,
				Kind:      oauth.TokenKindAccess,
				GrantID:   uuid.New(),
				ExpiresAt: time.Now().Add(oauth.MachineTokenTTL),
				RevokedAt: &revokedAt,
			},
		},
	}

	authn := NewMCPTokenAuthenticator(
		tokens,
		&fakeMemberByID{},
		fakeMemberships{},
		fakeOrgAdmin{},
		testMCPResourceURL,
		&fakeFallback{},
		slog.New(slog.DiscardHandler),
	)

	if _, err := authn.Authenticate(t.Context(), "Bearer "+raw); err == nil {
		t.Fatal("a revoked machine token must be rejected")
	}
}
