// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

func TestStoreClientRoundTrip(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := NewStore(pool)

	client := Client{
		ID:            "mcp_client_" + uuid.NewString(),
		Name:          "Claude Code (test)",
		RedirectURIs:  []string{"http://localhost:3118/callback"},
		GrantTypes:    []string{"authorization_code", "refresh_token"},
		ResponseTypes: []string{"code"},
		AuthMethod:    "none",
	}

	if err := store.CreateClient(t.Context(), client); err != nil {
		t.Fatalf("CreateClient: %v", err)
	}

	got, err := store.GetClient(t.Context(), client.ID)
	if err != nil {
		t.Fatalf("GetClient: %v", err)
	}

	if got.Name != client.Name || len(got.RedirectURIs) != 1 || got.RedirectURIs[0] != client.RedirectURIs[0] {
		t.Errorf("GetClient round trip mismatch: got %+v", got)
	}

	if !got.HasRedirectURI("http://localhost:3118/callback") {
		t.Error("HasRedirectURI must match the registered URI exactly")
	}

	if _, err := store.GetClient(t.Context(), "unknown"); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("unknown client_id: got %v, want ErrNotFound", err)
	}
}

func TestStoreAuthCodeSingleUseAndExpiry(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := NewStore(pool)
	memberID := seedMember(t, pool)
	client := seedClient(t, store)

	code := AuthCode{
		ID:                  uuid.New(),
		ClientID:            client.ID,
		RedirectURI:         client.RedirectURIs[0],
		CodeChallenge:       "challenge",
		CodeChallengeMethod: "S256",
		Resource:            "https://orako.example.com/mcp",
		MemberID:            memberID,
		ExpiresAt:           time.Now().Add(2 * time.Minute),
	}

	raw := "test-code-" + uuid.NewString()

	if err := store.CreateAuthCode(t.Context(), code, HashSecret(raw)); err != nil {
		t.Fatalf("CreateAuthCode: %v", err)
	}

	got, err := store.ConsumeAuthCode(t.Context(), raw)
	if err != nil {
		t.Fatalf("ConsumeAuthCode: %v", err)
	}

	if got.MemberID != memberID || got.ClientID != client.ID {
		t.Errorf("ConsumeAuthCode mismatch: got %+v", got)
	}

	if _, err := store.ConsumeAuthCode(t.Context(), raw); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("re-consuming a used code: got %v, want ErrNotFound", err)
	}

	expiredCode := AuthCode{
		ID:                  uuid.New(),
		ClientID:            client.ID,
		RedirectURI:         client.RedirectURIs[0],
		CodeChallenge:       "challenge",
		CodeChallengeMethod: "S256",
		Resource:            "https://orako.example.com/mcp",
		MemberID:            memberID,
		ExpiresAt:           time.Now().Add(-time.Second),
	}

	expiredRaw := "expired-code-" + uuid.NewString()

	if err := store.CreateAuthCode(t.Context(), expiredCode, HashSecret(expiredRaw)); err != nil {
		t.Fatalf("CreateAuthCode (expired): %v", err)
	}

	if _, err := store.ConsumeAuthCode(t.Context(), expiredRaw); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("consuming an expired code: got %v, want ErrNotFound", err)
	}
}

// TestStoreProjectScopeRoundTrip proves a chosen project scope survives the
// auth-code → token chain with order preserved (first = primary), that the
// refresh row carries it (so rotation can preserve it), and that an unscoped
// code round-trips to an empty scope (the legacy "all projects" default).
func TestStoreProjectScopeRoundTrip(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := NewStore(pool)
	memberID := seedMember(t, pool)
	client := seedClient(t, store)
	resource := "https://orako.example.com/mcp"
	p1, p2 := uuid.New(), uuid.New()

	code := AuthCode{
		ID:                  uuid.New(),
		ClientID:            client.ID,
		RedirectURI:         client.RedirectURIs[0],
		CodeChallenge:       "challenge",
		CodeChallengeMethod: "S256",
		Resource:            resource,
		MemberID:            memberID,
		ProjectIDs:          []uuid.UUID{p1, p2},
		ExpiresAt:           time.Now().Add(2 * time.Minute),
	}

	raw := "scoped-code-" + uuid.NewString()
	if err := store.CreateAuthCode(t.Context(), code, HashSecret(raw)); err != nil {
		t.Fatalf("CreateAuthCode: %v", err)
	}

	gotCode, err := store.ConsumeAuthCode(t.Context(), raw)
	if err != nil {
		t.Fatalf("ConsumeAuthCode: %v", err)
	}

	if len(gotCode.ProjectIDs) != 2 || gotCode.ProjectIDs[0] != p1 || gotCode.ProjectIDs[1] != p2 {
		t.Fatalf("auth-code scope: got %v, want [%s %s]", gotCode.ProjectIDs, p1, p2)
	}

	grantID := uuid.New()
	access := Token{
		ID: uuid.New(), MemberID: memberID, ClientID: client.ID, Resource: resource,
		Kind: TokenKindAccess, ProjectIDs: gotCode.ProjectIDs, GrantID: grantID,
		ExpiresAt: time.Now().Add(AccessTokenTTL),
	}
	refresh := Token{
		ID: uuid.New(), MemberID: memberID, ClientID: client.ID, Resource: resource,
		Kind: TokenKindRefresh, ProjectIDs: gotCode.ProjectIDs, GrantID: grantID,
		ExpiresAt: time.Now().Add(RefreshTokenTTL),
	}

	accessRaw := "scoped-access-" + uuid.NewString()
	refreshRaw := "scoped-refresh-" + uuid.NewString()
	if err := store.CreateTokenPair(t.Context(), access, HashSecret(accessRaw), refresh, HashSecret(refreshRaw)); err != nil {
		t.Fatalf("CreateTokenPair: %v", err)
	}

	gotAccess, err := store.GetToken(t.Context(), accessRaw, TokenKindAccess)
	if err != nil {
		t.Fatalf("GetToken(access): %v", err)
	}

	if len(gotAccess.ProjectIDs) != 2 || gotAccess.ProjectIDs[0] != p1 || gotAccess.ProjectIDs[1] != p2 {
		t.Errorf("token scope: got %v, want [%s %s]", gotAccess.ProjectIDs, p1, p2)
	}

	gotRefresh, err := store.GetToken(t.Context(), refreshRaw, TokenKindRefresh)
	if err != nil {
		t.Fatalf("GetToken(refresh): %v", err)
	}

	if len(gotRefresh.ProjectIDs) != 2 {
		t.Errorf("refresh scope: got %v, want 2 ids (rotation must preserve it)", gotRefresh.ProjectIDs)
	}

	unscoped := AuthCode{
		ID:                  uuid.New(),
		ClientID:            client.ID,
		RedirectURI:         client.RedirectURIs[0],
		CodeChallenge:       "challenge",
		CodeChallengeMethod: "S256",
		Resource:            resource,
		MemberID:            memberID,
		ExpiresAt:           time.Now().Add(2 * time.Minute),
	}
	unscopedRaw := "unscoped-code-" + uuid.NewString()
	if err := store.CreateAuthCode(t.Context(), unscoped, HashSecret(unscopedRaw)); err != nil {
		t.Fatalf("CreateAuthCode (unscoped): %v", err)
	}

	gotUnscoped, err := store.ConsumeAuthCode(t.Context(), unscopedRaw)
	if err != nil {
		t.Fatalf("ConsumeAuthCode (unscoped): %v", err)
	}

	if len(gotUnscoped.ProjectIDs) != 0 {
		t.Errorf("unscoped code must round-trip to empty, got %v", gotUnscoped.ProjectIDs)
	}
}

func TestStoreTokenPairRevokeAndTouch(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := NewStore(pool)
	memberID := seedMember(t, pool)
	client := seedClient(t, store)
	resource := "https://orako.example.com/mcp"
	grantID := uuid.New()

	access := Token{
		ID:        uuid.New(),
		MemberID:  memberID,
		ClientID:  client.ID,
		Resource:  resource,
		Kind:      TokenKindAccess,
		GrantID:   grantID,
		ExpiresAt: time.Now().Add(AccessTokenTTL),
	}
	refresh := Token{
		ID:        uuid.New(),
		MemberID:  memberID,
		ClientID:  client.ID,
		Resource:  resource,
		Kind:      TokenKindRefresh,
		GrantID:   grantID,
		ExpiresAt: time.Now().Add(RefreshTokenTTL),
	}

	accessRaw := "access-" + uuid.NewString()
	refreshRaw := "refresh-" + uuid.NewString()

	if err := store.CreateTokenPair(t.Context(), access, HashSecret(accessRaw), refresh, HashSecret(refreshRaw)); err != nil {
		t.Fatalf("CreateTokenPair: %v", err)
	}

	gotAccess, err := store.GetToken(t.Context(), accessRaw, TokenKindAccess)
	if err != nil {
		t.Fatalf("GetToken(access): %v", err)
	}

	if gotAccess.Revoked() || gotAccess.MemberID != memberID {
		t.Errorf("GetToken(access) mismatch: got %+v", gotAccess)
	}

	if _, err := store.GetToken(t.Context(), accessRaw, TokenKindRefresh); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("requesting the access secret as a refresh token: got %v, want ErrNotFound", err)
	}

	if err := store.TouchToken(t.Context(), gotAccess.ID); err != nil {
		t.Fatalf("TouchToken: %v", err)
	}

	touched, err := store.GetToken(t.Context(), accessRaw, TokenKindAccess)
	if err != nil {
		t.Fatalf("GetToken after touch: %v", err)
	}

	if touched.LastUsedAt == nil {
		t.Error("TouchToken did not stamp last_used_at")
	}

	if err := store.RevokeToken(t.Context(), gotAccess.ID); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	revoked, err := store.GetToken(t.Context(), accessRaw, TokenKindAccess)
	if err != nil {
		t.Fatalf("GetToken after revoke: %v", err)
	}

	if !revoked.Revoked() {
		t.Error("RevokeToken did not set revoked_at")
	}

	// RevokeGrant must invalidate every token sharing grantID, including the
	// refresh token that was never individually revoked.
	if err := store.RevokeGrant(t.Context(), grantID); err != nil {
		t.Fatalf("RevokeGrant: %v", err)
	}

	gotRefresh, err := store.GetToken(t.Context(), refreshRaw, TokenKindRefresh)
	if err != nil {
		t.Fatalf("GetToken(refresh) after RevokeGrant: %v", err)
	}

	if !gotRefresh.Revoked() {
		t.Error("RevokeGrant did not revoke the refresh token sharing the grant")
	}
}

func TestStoreListGrantsByMember(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := NewStore(pool)
	memberID := seedMember(t, pool)
	otherMemberID := seedMember(t, pool)
	client := seedClient(t, store)
	resource := "https://orako.example.com/mcp"

	// A live grant for memberID: scoped to two projects.
	p1, p2 := uuid.New(), uuid.New()
	liveGrant := uuid.New()
	seedTokenPair(t, store, memberID, client.ID, resource, liveGrant, []uuid.UUID{p1, p2})

	// A fully revoked grant for memberID must not appear.
	deadGrant := uuid.New()
	seedTokenPair(t, store, memberID, client.ID, resource, deadGrant, nil)
	if err := store.RevokeGrant(t.Context(), deadGrant); err != nil {
		t.Fatalf("RevokeGrant (dead grant setup): %v", err)
	}

	// A live grant belonging to a different member must not appear.
	otherGrant := uuid.New()
	seedTokenPair(t, store, otherMemberID, client.ID, resource, otherGrant, nil)

	grants, err := store.ListGrantsByMember(t.Context(), memberID)
	if err != nil {
		t.Fatalf("ListGrantsByMember: %v", err)
	}

	if len(grants) != 1 {
		t.Fatalf("ListGrantsByMember: got %d grants, want 1 (dead+other-member grants must be excluded): %+v", len(grants), grants)
	}

	got := grants[0]
	if got.GrantID != liveGrant {
		t.Errorf("GrantID = %s, want %s", got.GrantID, liveGrant)
	}
	if got.ClientName != client.Name {
		t.Errorf("ClientName = %q, want %q", got.ClientName, client.Name)
	}
	if got.Resource != resource {
		t.Errorf("Resource = %q, want %q", got.Resource, resource)
	}
	if len(got.ProjectIDs) != 2 || got.ProjectIDs[0] != p1 || got.ProjectIDs[1] != p2 {
		t.Errorf("ProjectIDs = %v, want [%s %s]", got.ProjectIDs, p1, p2)
	}
	if got.LastUsedAt != nil {
		t.Errorf("LastUsedAt = %v, want nil (never touched)", got.LastUsedAt)
	}
	if got.ExpiresAt.IsZero() {
		t.Error("ExpiresAt must be set")
	}
}

// seedTokenPair mints and persists an access+refresh token pair sharing
// grantID for memberID, returning their raw secrets. A nil projectIDs means
// an unscoped grant.
func seedTokenPair(t *testing.T, store *Store, memberID uuid.UUID, clientID, resource string, grantID uuid.UUID, projectIDs []uuid.UUID) (accessRaw, refreshRaw string) {
	t.Helper()

	access := Token{
		ID: uuid.New(), MemberID: memberID, ClientID: clientID, Resource: resource,
		Kind: TokenKindAccess, ProjectIDs: projectIDs, GrantID: grantID,
		ExpiresAt: time.Now().Add(AccessTokenTTL),
	}
	refresh := Token{
		ID: uuid.New(), MemberID: memberID, ClientID: clientID, Resource: resource,
		Kind: TokenKindRefresh, ProjectIDs: projectIDs, GrantID: grantID,
		ExpiresAt: time.Now().Add(RefreshTokenTTL),
	}

	accessRaw = "grant-access-" + uuid.NewString()
	refreshRaw = "grant-refresh-" + uuid.NewString()

	if err := store.CreateTokenPair(t.Context(), access, HashSecret(accessRaw), refresh, HashSecret(refreshRaw)); err != nil {
		t.Fatalf("seedTokenPair: %v", err)
	}

	return accessRaw, refreshRaw
}

func TestStoreListGrantsByMemberLastUsed(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := NewStore(pool)
	memberID := seedMember(t, pool)
	client := seedClient(t, store)
	resource := "https://orako.example.com/mcp"
	grantID := uuid.New()

	accessRaw, _ := seedTokenPair(t, store, memberID, client.ID, resource, grantID, nil)

	access, err := store.GetToken(t.Context(), accessRaw, TokenKindAccess)
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}

	if err := store.TouchToken(t.Context(), access.ID); err != nil {
		t.Fatalf("TouchToken: %v", err)
	}

	grants, err := store.ListGrantsByMember(t.Context(), memberID)
	if err != nil {
		t.Fatalf("ListGrantsByMember: %v", err)
	}

	if len(grants) != 1 {
		t.Fatalf("got %d grants, want 1", len(grants))
	}

	if grants[0].LastUsedAt == nil {
		t.Error("LastUsedAt must be set after TouchToken on the grant's access token")
	}
}

func TestStoreRevokeGrantForMember(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := NewStore(pool)
	memberID := seedMember(t, pool)
	otherMemberID := seedMember(t, pool)
	client := seedClient(t, store)
	resource := "https://orako.example.com/mcp"
	grantID := uuid.New()

	accessRaw, refreshRaw := seedTokenPair(t, store, memberID, client.ID, resource, grantID, nil)

	// SECURITY: another member must never be able to revoke this grant.
	if err := store.RevokeGrantForMember(t.Context(), otherMemberID, grantID); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("RevokeGrantForMember by wrong member: got %v, want ErrNotFound", err)
	}

	access, err := store.GetToken(t.Context(), accessRaw, TokenKindAccess)
	if err != nil {
		t.Fatalf("GetToken(access): %v", err)
	}
	if access.Revoked() {
		t.Fatal("grant must still be live after a wrongly-scoped revoke attempt")
	}

	if err := store.RevokeGrantForMember(t.Context(), memberID, grantID); err != nil {
		t.Fatalf("RevokeGrantForMember: %v", err)
	}

	gotAccess, err := store.GetToken(t.Context(), accessRaw, TokenKindAccess)
	if err != nil {
		t.Fatalf("GetToken(access) after revoke: %v", err)
	}
	if !gotAccess.Revoked() {
		t.Error("RevokeGrantForMember did not revoke the access token")
	}

	gotRefresh, err := store.GetToken(t.Context(), refreshRaw, TokenKindRefresh)
	if err != nil {
		t.Fatalf("GetToken(refresh) after revoke: %v", err)
	}
	if !gotRefresh.Revoked() {
		t.Error("RevokeGrantForMember did not revoke the refresh token sharing the grant")
	}

	// Revoking again (already revoked) must be not-found, not a silent no-op.
	if err := store.RevokeGrantForMember(t.Context(), memberID, grantID); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("RevokeGrantForMember on an already-revoked grant: got %v, want ErrNotFound", err)
	}

	// The revoked grant must disappear from the member's live list.
	grants, err := store.ListGrantsByMember(t.Context(), memberID)
	if err != nil {
		t.Fatalf("ListGrantsByMember: %v", err)
	}
	for _, g := range grants {
		if g.GrantID == grantID {
			t.Errorf("revoked grant %s must not appear in ListGrantsByMember", grantID)
		}
	}
}

// seedClient registers a minimal client directly through the store, for
// tests that need a valid client_id/redirect_uri pair but aren't testing
// registration itself.
func seedClient(t *testing.T, store *Store) Client {
	t.Helper()

	client := Client{
		ID:            "mcp_client_" + uuid.NewString(),
		Name:          "Test Client",
		RedirectURIs:  []string{"http://localhost:3118/callback"},
		GrantTypes:    []string{"authorization_code", "refresh_token"},
		ResponseTypes: []string{"code"},
		AuthMethod:    "none",
	}

	if err := store.CreateClient(t.Context(), client); err != nil {
		t.Fatalf("seedClient: %v", err)
	}

	return client
}
