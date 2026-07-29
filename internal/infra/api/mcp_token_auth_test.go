// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/infra/api/oauth"
)

const testMCPResourceURL = "https://orako.example.com/mcp"

// fakeMCPTokenStore is an in-memory mcpTokenReader keyed by raw secret.
type fakeMCPTokenStore struct {
	tokens  map[string]oauth.Token
	touched []uuid.UUID
}

func (f *fakeMCPTokenStore) GetToken(_ context.Context, rawToken string, kind oauth.TokenKind) (oauth.Token, error) {
	tok, ok := f.tokens[rawToken]
	if !ok || tok.Kind != kind {
		return oauth.Token{}, adaptererr.ErrNotFound
	}

	return tok, nil
}

func (f *fakeMCPTokenStore) TouchToken(_ context.Context, id uuid.UUID) error {
	f.touched = append(f.touched, id)

	return nil
}

// newMCPTokenFixture builds an MCPTokenAuthenticator over one valid access
// token, a member with an org-admin membership, and a fallback that reports
// whether it was reached.
func newMCPTokenFixture(t *testing.T) (*fakeMCPTokenStore, *fakeFallback, MCPTokenAuthenticator, string, uuid.UUID) {
	t.Helper()

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
				ClientID:  "mcp_client_test",
				Resource:  testMCPResourceURL,
				Kind:      oauth.TokenKindAccess,
				GrantID:   uuid.New(),
				ExpiresAt: time.Now().Add(time.Hour),
			},
		},
	}

	fallback := &fakeFallback{}

	authn := NewMCPTokenAuthenticator(
		tokens,
		&fakeMemberByID{member: model.Member{ID: memberID, AccountID: accountID, Status: model.MemberStatusActive}},
		fakeMemberships{rows: []repository.ProjectWithRole{{ID: projectID, OrgID: orgID}}},
		fakeOrgAdmin{role: model.OrgRoleAdmin},
		testMCPResourceURL,
		fallback,
		slog.New(slog.DiscardHandler),
	)

	return tokens, fallback, authn, raw, memberID
}

func TestMCPTokenAuthenticatesAndTouches(t *testing.T) {
	t.Parallel()

	tokens, fallback, authn, raw, memberID := newMCPTokenFixture(t)

	identity, err := authn.Authenticate(t.Context(), "Bearer "+raw)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	if identity.MemberID != memberID {
		t.Errorf("identity member = %s, want %s", identity.MemberID, memberID)
	}

	if identity.ProjectID == uuid.Nil || !identity.IsOrgAdmin {
		t.Errorf("membership/org-admin not resolved: %+v", identity)
	}

	if len(tokens.touched) != 1 {
		t.Errorf("last_used_at not stamped")
	}

	if fallback.called {
		t.Error("fallback must not run for mcp_at_ bearers")
	}
}

func TestMCPTokenUnknownRejected(t *testing.T) {
	t.Parallel()

	_, _, authn, _, _ := newMCPTokenFixture(t)

	if _, err := authn.Authenticate(t.Context(), "Bearer mcp_at_"+uuid.NewString()); err == nil {
		t.Fatal("an unknown mcp token must be rejected")
	}
}

func TestMCPTokenRevokedRejected(t *testing.T) {
	t.Parallel()

	tokens, _, authn, raw, _ := newMCPTokenFixture(t)

	tok := tokens.tokens[raw]
	revokedAt := time.Now()
	tok.RevokedAt = &revokedAt
	tokens.tokens[raw] = tok

	if _, err := authn.Authenticate(t.Context(), "Bearer "+raw); err == nil {
		t.Fatal("a revoked mcp token must be rejected")
	}
}

func TestMCPTokenExpiredRejected(t *testing.T) {
	t.Parallel()

	tokens, _, authn, raw, _ := newMCPTokenFixture(t)

	tok := tokens.tokens[raw]
	tok.ExpiresAt = time.Now().Add(-time.Minute)
	tokens.tokens[raw] = tok

	_, err := authn.Authenticate(t.Context(), "Bearer "+raw)
	if err == nil {
		t.Fatal("an expired mcp token must be rejected")
	}
}

func TestMCPTokenWrongAudienceRejected(t *testing.T) {
	t.Parallel()

	tokens, _, authn, raw, _ := newMCPTokenFixture(t)

	tok := tokens.tokens[raw]
	tok.Resource = "https://attacker.example.com/mcp"
	tokens.tokens[raw] = tok

	if _, err := authn.Authenticate(t.Context(), "Bearer "+raw); err == nil {
		t.Fatal("a token minted for a different resource must be rejected (confused-deputy guard)")
	}
}

// TestMCPTokenRawJWTRejectedWithRejectingFallback proves the resource-server
// posture phase 3's /mcp mount must use: wired with a fallback that always
// rejects (never the dashboard's dev/jwt chain), a raw bearer that isn't an
// mcp_at_ secret — e.g. a real Supabase JWT, valid for the Connect-RPC
// dashboard surface — is never accepted here. No confused-deputy reuse of a
// dashboard session token to call an MCP tool.
func TestMCPTokenRawJWTRejectedWithRejectingFallback(t *testing.T) {
	t.Parallel()

	_, _, authn, _, _ := newMCPTokenFixture(t)

	rejecting := rejectingFallback{}
	authn.fallback = rejecting

	rawJWT := "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJ1c2VyXzEifQ.sig"

	if _, err := authn.Authenticate(t.Context(), "Bearer "+rawJWT); err == nil {
		t.Fatal("a raw JWT must be rejected when /mcp is wired with a rejecting fallback")
	}
}

// TestNonMCPBearerFallsThrough documents the Connect-RPC wiring convenience:
// a non-mcp_at_ bearer (JWT, dev-stub) is handed to fallback unchanged, so
// main.go's chain wiring lets an mcp_at_ token ALSO work
// against the dashboard API without this authenticator interfering with
// every other bearer shape.
func TestNonMCPBearerFallsThrough(t *testing.T) {
	t.Parallel()

	_, fallback, authn, _, _ := newMCPTokenFixture(t)

	if _, err := authn.Authenticate(t.Context(), "Bearer eyJhbGciOiJFUzI1NiJ9.x.y"); err != nil {
		t.Fatalf("fallback path errored: %v", err)
	}

	if !fallback.called {
		t.Error("non-mcp_at_ bearer must reach the wrapped authenticator")
	}
}

// rejectingFallback simulates the /mcp resource-server wiring: no fallback
// identity source at all, every non-mcp_at_ bearer is rejected outright.
type rejectingFallback struct{}

func (rejectingFallback) Authenticate(_ context.Context, _ string) (CallerIdentity, error) {
	return CallerIdentity{}, errRejectingFallback
}

func (rejectingFallback) AuthenticateAccount(_ context.Context, _ string) (CallerIdentity, error) {
	return CallerIdentity{}, errRejectingFallback
}

var errRejectingFallback = errors.New("no identity available for this bearer")
