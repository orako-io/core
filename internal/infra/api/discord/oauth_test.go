// SPDX-License-Identifier: AGPL-3.0-or-later

package discord_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	discordhttp "github.com/orako-io/core/internal/infra/api/discord"
	"github.com/orako-io/core/internal/pkg/errs"
)

type fakeCreds struct {
	id, secret string
	// botToken and anchorChannel drive GuildJoinContext. Both empty (the zero
	// value used by the OAuth-only tests) means "skip the auto-join".
	botToken, anchorChannel string
}

func (f fakeCreds) DiscordOAuthCreds(context.Context, uuid.UUID) (string, string, error) {
	return f.id, f.secret, nil
}

func (f fakeCreds) GuildJoinContext(context.Context, uuid.UUID) (string, string, error) {
	return f.botToken, f.anchorChannel, nil
}

type fakeAuth struct {
	memberID  uuid.UUID
	projectID uuid.UUID
	ok        bool
}

func (f fakeAuth) Authenticate(_ context.Context, header string) (uuid.UUID, uuid.UUID, error) {
	if !f.ok || header == "" {
		return uuid.Nil, uuid.Nil, http.ErrNoCookie
	}

	return f.memberID, f.projectID, nil
}

type recordingBinder struct {
	memberID uuid.UUID
	discord  string
}

func (b *recordingBinder) BindDiscord(_ context.Context, memberID uuid.UUID, discordUserID string) error {
	b.memberID = memberID
	b.discord = discordUserID

	return nil
}

// conflictBinder rejects every bind with a duplicate error, standing in for a
// Discord snowflake already linked to another member (the UNIQUE violation the
// command layer translates to errs.DuplicateError).
type conflictBinder struct{}

func (conflictBinder) BindDiscord(context.Context, uuid.UUID, string) error {
	return errs.DuplicateError{Resource: "member", Field: "Discord user ID"}
}

// discordAPIStub mocks /oauth2/token and /users/@me.
func discordAPIStub(t *testing.T, snowflake string) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "at-123"})
	})
	mux.HandleFunc("/users/@me", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"id": snowflake})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

// TestInstall_RequiresAuth proves the install endpoint rejects an anonymous
// caller and, when authenticated, returns an authorize URL carrying a state.
func TestInstall_RequiresAuth(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	h := discordhttp.NewOAuthHandler(
		discordhttp.OAuthConfig{BaseURL: "https://app.orako.io", APIBase: "https://discord.example"},
		fakeCreds{id: "client-1", secret: "secret-1"},
		fakeAuth{memberID: memberID, projectID: uuid.New(), ok: true},
		&recordingBinder{},
	)

	// No Authorization → 401.
	rNoAuth := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/discord/oauth/install", nil)
	wNoAuth := httptest.NewRecorder()
	h.ServeInstall(wNoAuth, rNoAuth)

	if wNoAuth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated install = %d, want 401", wNoAuth.Code)
	}

	// With Authorization → 200 + authorize_url with a state.
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/discord/oauth/install", nil)
	r.Header.Set("Authorization", "Bearer x")
	w := httptest.NewRecorder()
	h.ServeInstall(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("authenticated install = %d, want 200", w.Code)
	}

	var body struct {
		AuthorizeURL string `json:"authorize_url"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decoding install response: %v", err)
	}

	u, err := url.Parse(body.AuthorizeURL)
	if err != nil || u.Query().Get("state") == "" || u.Query().Get("scope") != "identify guilds.join" {
		t.Fatalf("authorize_url missing state/scope: %q", body.AuthorizeURL)
	}
}

// TestCallback_BindsMember proves the callback exchanges the code, reads
// /users/@me, and binds the snowflake to the member the state was minted for.
func TestCallback_BindsMember(t *testing.T) {
	t.Parallel()

	api := discordAPIStub(t, "snow-999")
	memberID := uuid.New()
	binder := &recordingBinder{}

	h := discordhttp.NewOAuthHandler(
		discordhttp.OAuthConfig{BaseURL: "https://app.orako.io", APIBase: api.URL},
		fakeCreds{id: "client-1", secret: "secret-1"},
		fakeAuth{memberID: memberID, projectID: uuid.New(), ok: true},
		binder,
	)

	// Mint a real state via install.
	rInstall := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/discord/oauth/install", nil)
	rInstall.Header.Set("Authorization", "Bearer x")
	wInstall := httptest.NewRecorder()
	h.ServeInstall(wInstall, rInstall)

	var body struct {
		AuthorizeURL string `json:"authorize_url"`
	}
	_ = json.NewDecoder(wInstall.Body).Decode(&body)
	u, _ := url.Parse(body.AuthorizeURL)
	state := u.Query().Get("state")

	// Callback with that state + a code.
	rCb := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/discord/oauth/callback?code=abc&state="+state, nil)
	wCb := httptest.NewRecorder()
	h.ServeCallback(wCb, rCb)

	if wCb.Code != http.StatusOK {
		t.Fatalf("callback = %d, want 200; body: %s", wCb.Code, wCb.Body.String())
	}

	if binder.memberID != memberID || binder.discord != "snow-999" {
		t.Fatalf("bound %s→%q, want %s→snow-999", binder.memberID, binder.discord, memberID)
	}

	// The state is single-use: replaying it fails.
	wReplay := httptest.NewRecorder()
	h.ServeCallback(wReplay, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/discord/oauth/callback?code=abc&state="+state, nil))
	if wReplay.Code == http.StatusOK {
		t.Errorf("replayed state must not succeed, got %d", wReplay.Code)
	}
}

// guildAPIStub extends the OAuth stub with the two guild endpoints the
// auto-join touches: GET /channels/{id} → guild_id, and PUT
// /guilds/{guild}/members/{user}. It records the access_token the guild-add was
// called with so the test can assert the USER token (not the bot token) is sent.
type guildAPIStub struct {
	server     *httptest.Server
	guildID    string
	joinStatus int
	// gotAccessToken is the body's access_token seen by the PUT (empty until hit).
	gotAccessToken string
	putHit         bool
}

func newGuildAPIStub(t *testing.T, snowflake, channelID, guildID string, joinStatus int) *guildAPIStub {
	t.Helper()

	stub := &guildAPIStub{server: nil, guildID: guildID, joinStatus: joinStatus, gotAccessToken: "", putHit: false}

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "user-token-abc"})
	})
	mux.HandleFunc("/users/@me", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"id": snowflake})
	})
	mux.HandleFunc("/channels/"+channelID, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"guild_id": guildID})
	})
	mux.HandleFunc("/guilds/"+guildID+"/members/"+snowflake, func(w http.ResponseWriter, r *http.Request) {
		stub.putHit = true

		var body struct {
			AccessToken string `json:"access_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		stub.gotAccessToken = body.AccessToken

		w.WriteHeader(stub.joinStatus)
	})

	stub.server = httptest.NewServer(mux)
	t.Cleanup(stub.server.Close)

	return stub
}

// TestCallback_AutoJoinsGuild proves the callback, after binding the snowflake,
// resolves the guild from the anchor channel and PUTs the member into it with
// their USER access token — and reflects the join in the confirmation page.
func TestCallback_AutoJoinsGuild(t *testing.T) {
	t.Parallel()

	const (
		snowflake = "snow-777"
		channelID = "chan-1"
		guildID   = "guild-1"
	)

	stub := newGuildAPIStub(t, snowflake, channelID, guildID, http.StatusCreated)
	binder := &recordingBinder{}

	h := discordhttp.NewOAuthHandler(
		discordhttp.OAuthConfig{BaseURL: "https://app.orako.io", APIBase: stub.server.URL},
		fakeCreds{id: "client-1", secret: "secret-1", botToken: "bot-xyz", anchorChannel: channelID},
		fakeAuth{memberID: uuid.New(), projectID: uuid.New(), ok: true},
		binder,
	)

	state := mintState(t, h)

	rCb := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/discord/oauth/callback?code=abc&state="+state, nil)
	wCb := httptest.NewRecorder()
	h.ServeCallback(wCb, rCb)

	if wCb.Code != http.StatusOK {
		t.Fatalf("callback = %d, want 200; body: %s", wCb.Code, wCb.Body.String())
	}

	if !stub.putHit {
		t.Fatal("guild-add PUT was never called")
	}

	if stub.gotAccessToken != "user-token-abc" {
		t.Errorf("guild-add access_token = %q, want the user token user-token-abc", stub.gotAccessToken)
	}

	if !strings.Contains(wCb.Body.String(), "added to the server") {
		t.Errorf("confirmation page = %q, want it to reflect the server join", wCb.Body.String())
	}
}

// TestCallback_GuildJoinFallback proves that when the auto-join can't run (no
// bot token / anchor channel), the bind still succeeds and the page falls back
// to the plain "connected" wording — no guild-add attempted.
func TestCallback_GuildJoinFallback(t *testing.T) {
	t.Parallel()

	stub := newGuildAPIStub(t, "snow-888", "chan-x", "guild-x", http.StatusCreated)
	binder := &recordingBinder{}

	h := discordhttp.NewOAuthHandler(
		discordhttp.OAuthConfig{BaseURL: "https://app.orako.io", APIBase: stub.server.URL},
		// No botToken/anchorChannel → GuildJoinContext yields empties → skip.
		fakeCreds{id: "client-1", secret: "secret-1"},
		fakeAuth{memberID: uuid.New(), projectID: uuid.New(), ok: true},
		binder,
	)

	state := mintState(t, h)

	rCb := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/discord/oauth/callback?code=abc&state="+state, nil)
	wCb := httptest.NewRecorder()
	h.ServeCallback(wCb, rCb)

	if wCb.Code != http.StatusOK {
		t.Fatalf("callback = %d, want 200; body: %s", wCb.Code, wCb.Body.String())
	}

	if stub.putHit {
		t.Error("guild-add PUT must not run when the auto-join context is empty")
	}

	if binder.discord != "snow-888" {
		t.Errorf("bind = %q, want snow-888 (bind must succeed even without auto-join)", binder.discord)
	}

	if strings.Contains(wCb.Body.String(), "added to the server") {
		t.Errorf("confirmation page = %q, want the plain connected wording (no server join)", wCb.Body.String())
	}
}

// mintState runs ServeInstall and returns a fresh, valid state token — the
// shared setup for the callback tests.
func mintState(t *testing.T, h *discordhttp.OAuthHandler) string {
	t.Helper()

	rInstall := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/discord/oauth/install", nil)
	rInstall.Header.Set("Authorization", "Bearer x")
	wInstall := httptest.NewRecorder()
	h.ServeInstall(wInstall, rInstall)

	var body struct {
		AuthorizeURL string `json:"authorize_url"`
	}
	if err := json.NewDecoder(wInstall.Body).Decode(&body); err != nil {
		t.Fatalf("decoding install response: %v", err)
	}

	u, err := url.Parse(body.AuthorizeURL)
	if err != nil {
		t.Fatalf("parsing authorize url: %v", err)
	}

	return u.Query().Get("state")
}

// TestCallback_DuplicateSnowflakeConflict proves a snowflake already bound to
// another member surfaces as a 409 (not an opaque 500), with a user-actionable
// message — the collision path the UNIQUE constraint enforces.
func TestCallback_DuplicateSnowflakeConflict(t *testing.T) {
	t.Parallel()

	api := discordAPIStub(t, "snow-taken")

	h := discordhttp.NewOAuthHandler(
		discordhttp.OAuthConfig{BaseURL: "https://app.orako.io", APIBase: api.URL},
		fakeCreds{id: "client-1", secret: "secret-1"},
		fakeAuth{memberID: uuid.New(), projectID: uuid.New(), ok: true},
		conflictBinder{},
	)

	// Mint a real state via install.
	rInstall := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/discord/oauth/install", nil)
	rInstall.Header.Set("Authorization", "Bearer x")
	wInstall := httptest.NewRecorder()
	h.ServeInstall(wInstall, rInstall)

	var body struct {
		AuthorizeURL string `json:"authorize_url"`
	}
	_ = json.NewDecoder(wInstall.Body).Decode(&body)
	u, _ := url.Parse(body.AuthorizeURL)
	state := u.Query().Get("state")

	rCb := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/discord/oauth/callback?code=abc&state="+state, nil)
	wCb := httptest.NewRecorder()
	h.ServeCallback(wCb, rCb)

	if wCb.Code != http.StatusConflict {
		t.Fatalf("callback on a taken snowflake = %d, want 409; body: %s", wCb.Code, wCb.Body.String())
	}

	if !strings.Contains(wCb.Body.String(), "already linked") {
		t.Errorf("409 body = %q, want the friendly collision message", wCb.Body.String())
	}
}

// TestDeriveClientID proves the app id is decoded from the bot token's first
// segment (base64url of the application id).
func TestDeriveClientID(t *testing.T) {
	t.Parallel()

	// base64url("123456789012345678") as the first token segment.
	if got := discordhttp.DeriveClientID("MTIzNDU2Nzg5MDEyMzQ1Njc4.abc.def"); got != "123456789012345678" {
		t.Fatalf("DeriveClientID = %q, want 123456789012345678", got)
	}
}
