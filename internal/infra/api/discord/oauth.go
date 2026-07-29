// SPDX-License-Identifier: AGPL-3.0-or-later

// Package discord serves the member-level Discord OAuth `identify` flow that
// self-binds a member's Discord user id — no hand-typed snowflake. Unlike the
// Slack OAuth install (project-level, admin), this is member-level: the install
// endpoint is authenticated and the signed state carries the caller's member id
// so the (unauthenticated) callback binds it.
package discord

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

const (
	// defaultDiscordAPIBase is Discord's production API base.
	defaultDiscordAPIBase = "https://discord.com/api"

	// oauthStateTTL bounds how long a CSRF state token is valid.
	oauthStateTTL = 10 * time.Minute

	defaultHTTPTimeout = 15 * time.Second
)

// oauthScope requests the user's id (identify) AND permission to add them to a
// guild the bot manages (guilds.join). guilds.join is what lets ServeCallback
// best-effort drop the member into the org's Discord server right after binding;
// the consent screen asks for it, but it stays optional — a denied/absent scope
// just means the auto-join no-ops and the manual invite link is the fallback.
const oauthScope = "identify guilds.join"

// CredResolver returns the Discord app's OAuth client id and secret for the
// caller's project org. The client id is derivable from the bot token; the
// secret must have been stored (optional discord credential).
type CredResolver interface {
	DiscordOAuthCreds(ctx context.Context, projectID uuid.UUID) (clientID, clientSecret string, err error)
	// GuildJoinContext returns the project's Discord bot token and an anchor
	// channel id (its configured alert channel), from which the guild to auto-join
	// is derived. Either value empty — or an error — means "skip the auto-join",
	// and the wizard falls back to the manual community-invite link. It never
	// blocks the bind.
	GuildJoinContext(ctx context.Context, projectID uuid.UUID) (botToken, anchorChannelID string, err error)
}

// Authenticator verifies the install request's bearer and yields the caller.
type Authenticator interface {
	Authenticate(ctx context.Context, authorizationHeader string) (memberID, projectID uuid.UUID, err error)
}

// Binder persists a member's resolved Discord id.
type Binder interface {
	BindDiscord(ctx context.Context, memberID uuid.UUID, discordUserID string) error
}

// OAuthConfig holds the flow's environment.
type OAuthConfig struct {
	// BaseURL is Orako's externally reachable base URL, for the redirect_uri.
	BaseURL string
	// APIBase overrides the Discord API base (tests point it at httptest).
	APIBase string `exhaustruct:"optional"`
	// HTTPClient defaults to a client with a bounded request timeout when nil.
	HTTPClient *http.Client `exhaustruct:"optional"`
}

func (c OAuthConfig) apiBase() string {
	if c.APIBase != "" {
		return c.APIBase
	}

	return defaultDiscordAPIBase
}

func (c OAuthConfig) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}

	return &http.Client{Timeout: defaultHTTPTimeout}
}

// stateEntry binds a CSRF state token to the member it was minted for.
type stateEntry struct {
	memberID  uuid.UUID
	projectID uuid.UUID
	expiresAt time.Time
}

// OAuthHandler serves the Discord identify self-bind.
//
//	GET  /discord/oauth/install   — AUTHENTICATED; returns {authorize_url}
//	GET  /discord/oauth/callback  — exchanges the code and binds the member
type OAuthHandler struct {
	cfg    OAuthConfig
	creds  CredResolver
	auth   Authenticator
	binder Binder
	now    func() time.Time

	mu     sync.Mutex `exhaustruct:"optional"`
	states map[string]stateEntry
}

// NewOAuthHandler builds an OAuthHandler.
func NewOAuthHandler(cfg OAuthConfig, creds CredResolver, auth Authenticator, binder Binder) *OAuthHandler {
	return &OAuthHandler{
		cfg:    cfg,
		creds:  creds,
		auth:   auth,
		binder: binder,
		now:    time.Now,
		states: make(map[string]stateEntry),
	}
}

// RegisterRoutes mounts the OAuth endpoints.
func (h *OAuthHandler) RegisterRoutes(r chi.Router) {
	r.Get("/discord/oauth/install", h.ServeInstall)
	r.Get("/discord/oauth/callback", h.ServeCallback)
}

// ServeInstall authenticates the caller, mints a state bound to their member
// id, and returns the Discord authorize URL as JSON for the SPA to redirect to.
// Returning the URL (rather than a 302) keeps the flow bearer-authenticated: a
// full-page redirect could not carry the Authorization header.
func (h *OAuthHandler) ServeInstall(w http.ResponseWriter, r *http.Request) {
	memberID, projectID, err := h.auth.Authenticate(r.Context(), r.Header.Get("Authorization"))
	if err != nil || memberID == uuid.Nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	clientID, _, err := h.creds.DiscordOAuthCreds(r.Context(), projectID)
	if err != nil || clientID == "" {
		http.Error(w, "discord oauth is not configured for this project", http.StatusBadRequest)
		return
	}

	state, err := h.generateState(memberID, projectID)
	if err != nil {
		http.Error(w, "cannot generate state", http.StatusInternalServerError)
		return
	}

	authorizeURL := fmt.Sprintf(
		"%s/oauth2/authorize?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&state=%s",
		h.cfg.apiBase(),
		url.QueryEscape(clientID),
		url.QueryEscape(h.redirectURI()),
		url.QueryEscape(oauthScope),
		url.QueryEscape(state),
	)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"authorize_url": authorizeURL})
}

// ServeCallback consumes the state, exchanges the code for a token, reads the
// user's snowflake, and binds it to the member the state was minted for.
func (h *OAuthHandler) ServeCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	if errParam := q.Get("error"); errParam != "" {
		http.Error(w, "discord oauth denied: "+errParam, http.StatusBadRequest)
		return
	}

	entry, ok := h.consumeState(q.Get("state"))
	if !ok {
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	code := q.Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	clientID, clientSecret, err := h.creds.DiscordOAuthCreds(r.Context(), entry.projectID)
	if err != nil {
		http.Error(w, "discord oauth is not configured", http.StatusBadRequest)
		return
	}

	token, err := h.exchangeCode(r.Context(), clientID, clientSecret, code)
	if err != nil {
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}

	snowflake, err := h.currentUserID(r.Context(), token)
	if err != nil || snowflake == "" {
		http.Error(w, "could not read Discord identity", http.StatusBadGateway)
		return
	}

	if err := h.binder.BindDiscord(r.Context(), entry.memberID, snowflake); err != nil {
		// Never swallow the real cause: log it with the member + provider so a
		// failed self-bind is diagnosable from the server logs.
		slog.ErrorContext(r.Context(), "discord self-bind failed",
			slog.String("member_id", entry.memberID.String()),
			slog.String("provider", "discord"),
			slog.Any("error", err),
		)

		// A Discord account is one-member (UNIQUE snowflake): a collision is a
		// user-actionable 409, not an opaque 500.
		var dup errs.DuplicateError
		if errors.As(err, &dup) {
			http.Error(w, "This Discord account is already linked to another member.", http.StatusConflict)
			return
		}

		http.Error(w, "could not bind Discord account", http.StatusInternalServerError)

		return
	}

	// Best-effort: drop the member into the org's Discord guild so Orako can open
	// a private thread and @-mention them. Any failure (scope denied, bot missing
	// permission, guild unresolved) is non-fatal — the bind already succeeded and
	// the wizard's manual invite link is the fallback. token is the USER access
	// token from the code exchange, which guilds.join requires in the body.
	joined := h.tryJoinGuild(r.Context(), entry.projectID, snowflake, token)

	// A plain confirmation page; the SPA reopened this tab, the member closes it.
	// Its wording reflects whether the server-join happened, so the member knows
	// whether they still need to accept a manual invite.
	body := `<!doctype html><meta charset="utf-8"><body style="font-family:sans-serif;padding:2rem"><h2>Discord connected ✓</h2><p>You can close this tab — Orako will reach you on Discord now.</p></body>`
	if joined {
		body = `<!doctype html><meta charset="utf-8"><body style="font-family:sans-serif;padding:2rem"><h2>Connected &amp; added to the server ✓</h2><p>You can close this tab — Orako will open a private thread and reach you on Discord now.</p></body>`
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

// tryJoinGuild best-effort adds the just-bound member to the org's Discord
// guild. It resolves the guild from the project's anchor (alert) channel, then
// PUTs the member with their user access token. Every failure is swallowed
// (logged) and reported as "not joined" so the caller can prompt the manual
// invite instead — this must never turn a successful bind into an error.
func (h *OAuthHandler) tryJoinGuild(ctx context.Context, projectID uuid.UUID, userID, userAccessToken string) bool {
	botToken, anchorChannelID, err := h.creds.GuildJoinContext(ctx, projectID)
	if err != nil || botToken == "" || anchorChannelID == "" {
		return false
	}

	guildID := h.guildIDForChannel(ctx, botToken, anchorChannelID)
	if guildID == "" {
		return false
	}

	joined, err := h.addGuildMember(ctx, botToken, guildID, userID, userAccessToken)
	if err != nil {
		slog.WarnContext(ctx, "discord guild auto-join failed; manual invite is the fallback",
			slog.String("provider", "discord"),
			slog.String("guild_id", guildID),
			slog.String("discord_user_id", userID),
			slog.Any("error", err),
		)

		return false
	}

	return joined
}

// guildIDForChannel resolves the guild a channel belongs to via
// GET /channels/{id} (bot-authed). Returns "" on any failure — the caller then
// skips the auto-join. This is how the guild is derived without hardcoding it.
func (h *OAuthHandler) guildIDForChannel(ctx context.Context, botToken, channelID string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.cfg.apiBase()+"/channels/"+url.PathEscape(channelID), nil)
	if err != nil {
		return ""
	}

	req.Header.Set("Authorization", "Bot "+botToken)

	resp, err := h.cfg.httpClient().Do(req)
	if err != nil {
		return ""
	}

	defer resp.Body.Close() //nolint:errcheck // informational close error in defer

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var result struct {
		GuildID string `json:"guild_id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}

	return result.GuildID
}

// addGuildMember adds a user to a guild via PUT /guilds/{guild}/members/{user}
// with the bot token and the user's access token. Discord answers 201 (added)
// or 204 (already a member) — both count as joined. Any other status is a
// non-fatal miss reported to the caller.
func (h *OAuthHandler) addGuildMember(ctx context.Context, botToken, guildID, userID, userAccessToken string) (bool, error) {
	payload, err := json.Marshal(map[string]string{"access_token": userAccessToken})
	if err != nil {
		return false, fmt.Errorf("marshaling guild-add body: %w", err)
	}

	endpoint := fmt.Sprintf("%s/guilds/%s/members/%s", h.cfg.apiBase(), url.PathEscape(guildID), url.PathEscape(userID))

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return false, fmt.Errorf("building guild-add request: %w", err)
	}

	req.Header.Set("Authorization", "Bot "+botToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.cfg.httpClient().Do(req)
	if err != nil {
		return false, fmt.Errorf("guild-add request: %w", err)
	}

	defer resp.Body.Close() //nolint:errcheck // informational close error in defer

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusNoContent:
		return true, nil
	default:
		return false, fmt.Errorf("guild-add returned status %d", resp.StatusCode)
	}
}

// exchangeCode swaps the authorization code for an access token.
func (h *OAuthHandler) exchangeCode(ctx context.Context, clientID, clientSecret, code string) (string, error) {
	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {h.redirectURI()},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.cfg.apiBase()+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("building token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := h.cfg.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}

	defer resp.Body.Close() //nolint:errcheck // informational close error in defer

	var result struct {
		AccessToken string `json:"access_token"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}

	if result.AccessToken == "" {
		return "", errors.New("empty access token")
	}

	return result.AccessToken, nil
}

// currentUserID calls GET /users/@me with the access token and returns the id.
func (h *OAuthHandler) currentUserID(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.cfg.apiBase()+"/users/@me", nil)
	if err != nil {
		return "", fmt.Errorf("building /users/@me request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := h.cfg.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("/users/@me: %w", err)
	}

	defer resp.Body.Close() //nolint:errcheck // informational close error in defer

	var result struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding /users/@me: %w", err)
	}

	return result.ID, nil
}

func (h *OAuthHandler) redirectURI() string {
	return h.cfg.BaseURL + "/discord/oauth/callback"
}

// generateState mints a random, TTL'd, single-use state bound to the member.
func (h *OAuthHandler) generateState(memberID, projectID uuid.UUID) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating state: %w", err)
	}

	token := hex.EncodeToString(raw)

	h.mu.Lock()
	h.states[token] = stateEntry{memberID: memberID, projectID: projectID, expiresAt: h.now().Add(oauthStateTTL)}
	h.mu.Unlock()

	return token, nil
}

// consumeState validates and removes a state token (one-time use).
func (h *OAuthHandler) consumeState(token string) (stateEntry, bool) {
	if token == "" {
		return stateEntry{}, false //nolint:exhaustruct // zero value on the not-ok arm
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	entry, ok := h.states[token]
	if !ok {
		return stateEntry{}, false //nolint:exhaustruct // zero value on the not-ok arm
	}

	delete(h.states, token)

	if h.now().After(entry.expiresAt) {
		return stateEntry{}, false //nolint:exhaustruct // zero value on the not-ok arm
	}

	return entry, true
}

// DeriveClientID extracts a Discord app's public client id from its bot token
// (the first dot-segment is the base64url-encoded application id). Returns ""
// on any malformed token. Exported so the composition root can build a
// CredResolver without duplicating the derivation.
func DeriveClientID(botToken string) string {
	seg, _, _ := strings.Cut(botToken, ".")

	decoded, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		if decoded, err = base64.URLEncoding.DecodeString(seg); err != nil {
			return ""
		}
	}

	return string(decoded)
}
