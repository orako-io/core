// SPDX-License-Identifier: AGPL-3.0-or-later

package slack

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// defaultSlackBaseURL is the production Slack API base URL.
const defaultSlackBaseURL = "https://slack.com"

// oauthStateTTL is how long a CSRF state token remains valid.
const oauthStateTTL = 10 * time.Minute

// OAuthScopes are the bot-token scopes required by Orako.
// chat:write — send DMs; users:read.email — directory seed (see directory.go).
const OAuthScopes = "chat:write,users:read,users:read.email"

// SlackCredentials holds the per-project Slack OAuth credentials returned by
// oauth.v2.access and persisted to the project_providers table.
//
// bot_token and signing_secret are encrypted at rest by the provider store when
// ORAKO_ENCRYPTION_KEY is set (secretbox / A3); plaintext JSONB otherwise.
//
//nolint:revive // exported name stutters (slack.SlackCredentials); renaming would break external callers
type SlackCredentials struct {
	BotToken string `json:"bot_token"`
	TeamID   string `json:"team_id"`
	TeamName string `json:"team_name"`
	AppID    string `json:"app_id"`
}

// ProviderConfigurator is the port through which the OAuth callback registers
// a newly installed provider, so the callback goes through the CQRS command
// layer rather than writing to the store and registry directly.
type ProviderConfigurator interface {
	// Configure persists and activates a provider for projectID.
	Configure(ctx context.Context, projectID uuid.UUID, kind string, creds map[string]string) error
}

// InstallPrincipal is the authenticated human requesting a Slack installation.
type InstallPrincipal struct {
	AccountID  uuid.UUID `exhaustruct:"optional"`
	MemberID   uuid.UUID
	OrgID      uuid.UUID
	IsOrgAdmin bool
}

// InstallAuthorization is the trusted tenant scope approved for an install.
// Every field is bound into the one-time OAuth state.
type InstallAuthorization struct {
	MemberID  uuid.UUID
	OrgID     uuid.UUID
	ProjectID uuid.UUID
}

// InstallAuthenticator validates the install request's bearer credential.
type InstallAuthenticator interface {
	Authenticate(ctx context.Context, authorizationHeader string) (InstallPrincipal, error)
}

// InstallAuthorizer verifies that principal is an org admin and that the
// requested project belongs to that same organization.
type InstallAuthorizer interface {
	Authorize(ctx context.Context, principal InstallPrincipal, requestedProjectID uuid.UUID) (InstallAuthorization, error)
}

// OAuthConfig holds the Slack app credentials for the OAuth flow.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	// SigningSecret is used for inbound webhook verification after install.
	SigningSecret string
	// BaseURL is the Orako server's externally reachable base URL, used to
	// construct the redirect_uri for the OAuth callback.
	BaseURL string
	// SlackBaseURL overrides the Slack API base URL; tests point it at an
	// httptest server. Empty means production ("https://slack.com").
	SlackBaseURL string `exhaustruct:"optional"`
	// HTTPClient defaults to http.DefaultClient when nil.
	HTTPClient *http.Client `exhaustruct:"optional"`
	// InstallAuthenticator and InstallAuthorizer are mandatory at runtime. They
	// are optional in the struct only so the server composition can be migrated
	// without changing this package and fails closed while either is absent.
	InstallAuthenticator InstallAuthenticator `exhaustruct:"optional"`
	InstallAuthorizer    InstallAuthorizer    `exhaustruct:"optional"`
}

func (c OAuthConfig) slackBase() string {
	if c.SlackBaseURL != "" {
		return c.SlackBaseURL
	}

	return defaultSlackBaseURL
}

func (c OAuthConfig) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}

	return http.DefaultClient
}

// stateEntry is an entry in the in-memory CSRF state table.
type stateEntry struct {
	memberID  uuid.UUID
	orgID     uuid.UUID
	projectID uuid.UUID
	expiresAt time.Time
}

// OAuthHandler handles Slack app installation via OAuth v2.
//
// Routes:
//
//	GET  /slack/oauth/install?project_id={UUID}  — authenticated authorize URL
//	GET  /slack/oauth/callback?code=…&state=…   — exchanges token + configures provider
type OAuthHandler struct {
	cfg          OAuthConfig
	configurator ProviderConfigurator
	now          func() time.Time // injectable for testing

	mu     sync.Mutex            `exhaustruct:"optional"`
	states map[string]stateEntry // state-token → authenticated member + authorized tenant scope
}

// NewOAuthHandler builds an OAuthHandler.
func NewOAuthHandler(cfg OAuthConfig, configurator ProviderConfigurator) *OAuthHandler {
	return &OAuthHandler{
		cfg:          cfg,
		configurator: configurator,
		now:          time.Now,
		states:       make(map[string]stateEntry),
	}
}

// RegisterRoutes mounts the OAuth endpoints on the router.
func (h *OAuthHandler) RegisterRoutes(r chi.Router) {
	r.Get("/slack/oauth/install", h.ServeInstall)
	r.Get("/slack/oauth/callback", h.ServeCallback)
}

// ServeInstall authenticates and authorizes the requested project, then returns
// Slack's authorize URL. The SPA performs this request with its bearer before
// navigating because a browser-level redirect cannot carry Authorization.
func (h *OAuthHandler) ServeInstall(w http.ResponseWriter, r *http.Request) {
	authorization, failure, status := h.authorizeInstall(r)
	if status != 0 {
		http.Error(w, failure, status)
		return
	}

	state, err := h.generateState(authorization)
	if err != nil {
		http.Error(w, "cannot generate state", http.StatusInternalServerError)
		return
	}

	redirectURI := h.cfg.BaseURL + "/slack/oauth/callback"

	authURL := fmt.Sprintf(
		"%s/oauth/v2/authorize?client_id=%s&scope=%s&redirect_uri=%s&state=%s",
		h.cfg.slackBase(),
		url.QueryEscape(h.cfg.ClientID),
		url.QueryEscape(OAuthScopes),
		url.QueryEscape(redirectURI),
		url.QueryEscape(state),
	)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"authorize_url": authURL})
}

func (h *OAuthHandler) authorizeInstall(r *http.Request) (InstallAuthorization, string, int) {
	empty := InstallAuthorization{
		MemberID:  uuid.Nil,
		OrgID:     uuid.Nil,
		ProjectID: uuid.Nil,
	}

	if h.cfg.InstallAuthenticator == nil || h.cfg.InstallAuthorizer == nil {
		return empty, "slack oauth install authorization is not configured", http.StatusServiceUnavailable
	}

	principal, err := h.cfg.InstallAuthenticator.Authenticate(r.Context(), r.Header.Get("Authorization"))
	if err != nil || principal.MemberID == uuid.Nil {
		return empty, "unauthorized", http.StatusUnauthorized
	}

	if !principal.IsOrgAdmin || principal.OrgID == uuid.Nil {
		return empty, "forbidden", http.StatusForbidden
	}

	projectIDStr := r.URL.Query().Get("project_id")

	projectID, err := uuid.Parse(projectIDStr)
	if err != nil || projectID == uuid.Nil {
		return empty, "invalid project_id", http.StatusBadRequest
	}

	authorization, err := h.cfg.InstallAuthorizer.Authorize(r.Context(), principal, projectID)
	if err != nil ||
		authorization.MemberID != principal.MemberID ||
		authorization.OrgID != principal.OrgID ||
		authorization.ProjectID != projectID {
		return empty, "forbidden", http.StatusForbidden
	}

	return authorization, "", 0
}

// ServeCallback handles the Slack OAuth callback, exchanges the authorization
// code for a bot token, and calls the ConfigureProvider command to persist and
// activate the provider.
func (h *OAuthHandler) ServeCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	state := q.Get("state")
	code := q.Get("code")
	errParam := q.Get("error")

	if errParam != "" {
		http.Error(w, "oauth denied: "+errParam, http.StatusBadRequest)
		return
	}

	if state == "" {
		http.Error(w, "missing state parameter", http.StatusBadRequest)
		return
	}

	authorization, ok := h.consumeState(state)
	if !ok {
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	if code == "" {
		http.Error(w, "missing code parameter", http.StatusBadRequest)
		return
	}

	creds, err := h.exchangeCode(r.Context(), code)
	if err != nil {
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}

	// The signing_secret comes from the app-level OAuthConfig, not the exchange.
	if err := h.configurator.Configure(r.Context(), authorization.ProjectID, "slack", map[string]string{
		"bot_token":      creds.BotToken,
		"signing_secret": h.cfg.SigningSecret,
		"team_id":        creds.TeamID,
		"team_name":      creds.TeamName,
		"app_id":         creds.AppID,
	}); err != nil {
		http.Error(w, "failed to configure provider", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// oauthV2AccessResponse is the relevant subset of the Slack oauth.v2.access response.
type oauthV2AccessResponse struct {
	OK          bool   `json:"ok"`
	Error       string `json:"error"`
	AppID       string `json:"app_id"`
	AccessToken string `json:"access_token"`
	Team        struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"team"`
}

// exchangeCode calls oauth.v2.access to exchange an authorization code for a
// bot token.
func (h *OAuthHandler) exchangeCode(ctx context.Context, code string) (SlackCredentials, error) {
	params := url.Values{
		"client_id":     {h.cfg.ClientID},
		"client_secret": {h.cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {h.cfg.BaseURL + "/slack/oauth/callback"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.cfg.slackBase()+"/api/oauth.v2.access",
		bytes.NewBufferString(params.Encode()),
	)
	if err != nil {
		return SlackCredentials{}, fmt.Errorf("building token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := h.cfg.httpClient().Do(req)
	if err != nil {
		return SlackCredentials{}, fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body close errors are informational in defer

	var result oauthV2AccessResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return SlackCredentials{}, fmt.Errorf("decoding token response: %w", err)
	}

	if !result.OK {
		return SlackCredentials{}, fmt.Errorf("slack oauth error: %s", result.Error)
	}

	return SlackCredentials{
		BotToken: result.AccessToken,
		TeamID:   result.Team.ID,
		TeamName: result.Team.Name,
		AppID:    result.AppID,
	}, nil
}

// generateState creates and stores a cryptographically random state token.
func (h *OAuthHandler) generateState(authorization InstallAuthorization) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating state token: %w", err)
	}

	token := hex.EncodeToString(raw)
	entry := stateEntry{
		memberID:  authorization.MemberID,
		orgID:     authorization.OrgID,
		projectID: authorization.ProjectID,
		expiresAt: h.now().Add(oauthStateTTL),
	}

	h.mu.Lock()
	// Evict expired states so abandoned OAuth flows don't leak (L10).
	now := h.now()
	for t, e := range h.states {
		if now.After(e.expiresAt) {
			delete(h.states, t)
		}
	}

	h.states[token] = entry
	h.mu.Unlock()

	return token, nil
}

// consumeState validates and removes a state token, returning the authenticated
// member and authorized org/project scope; false if unknown or expired.
func (h *OAuthHandler) consumeState(token string) (InstallAuthorization, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	entry, ok := h.states[token]
	if !ok {
		return InstallAuthorization{
			MemberID:  uuid.Nil,
			OrgID:     uuid.Nil,
			ProjectID: uuid.Nil,
		}, false
	}

	// Always remove the token — one-time use regardless of expiry.
	delete(h.states, token)

	if h.now().After(entry.expiresAt) ||
		entry.memberID == uuid.Nil ||
		entry.orgID == uuid.Nil ||
		entry.projectID == uuid.Nil {
		return InstallAuthorization{
			MemberID:  uuid.Nil,
			OrgID:     uuid.Nil,
			ProjectID: uuid.Nil,
		}, false
	}

	return InstallAuthorization{
		MemberID:  entry.memberID,
		OrgID:     entry.orgID,
		ProjectID: entry.projectID,
	}, true
}
