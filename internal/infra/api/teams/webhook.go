// SPDX-License-Identifier: AGPL-3.0-or-later

// Package teams is the Microsoft Teams (Bot Framework) inbound HTTP
// transport: POST /teams/activities/{projectID}, JWT-verified against the Bot
// Framework's own OpenID metadata (never the generic, unauthenticated
// /webhook/{provider}/{projectID} route — see
// internal/infra/api/webhook.go, which rejects provider=="teams").
package teams

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/provider"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/service"
)

// defaultMetadataURL is the Bot Framework's OpenID Connect metadata document
// (Verified 2026-07-06). Its jwks_uri points at the signing keys used to
// verify every inbound Activity's Authorization: Bearer JWT.
const defaultMetadataURL = "https://login.botframework.com/v1/.well-known/openidconfiguration"

// botFrameworkIssuer is the required `iss` claim on every Bot Framework JWT
// (Verified 2026-07-06).
const botFrameworkIssuer = "https://api.botframework.com"

// httpTimeout bounds the OpenID metadata discovery fetch and every JWKS
// refetch the RemoteKeySet performs on encountering an unknown key id, so a
// stalled Microsoft endpoint cannot block webhook construction or signature
// verification indefinitely.
const httpTimeout = 10 * time.Second

// maxActivityBodyBytes caps the inbound Activity body this handler will read,
// so an oversized payload cannot exhaust memory. 1 MiB comfortably covers a
// Bot Framework Activity.
const maxActivityBodyBytes = 1 << 20 // 1 MiB

// httpClient is the shared, timeout-bound client used for the metadata fetch
// and (via oidc.ClientContext) every JWKS fetch the RemoteKeySet performs.
var httpClient = &http.Client{Timeout: httpTimeout} //nolint:gochecknoglobals // shared, timeout-bound client; package-level by design

// providerLookup resolves the project's Teams provider from the registry —
// this route is Teams-specific, so the kind is always
// provider.ProviderKindTeams, never "whichever provider the project has"
// (review fit#4: a project may also have Slack/Discord/Telegram configured).
type providerLookup interface {
	ForProjectKind(ctx context.Context, projectID uuid.UUID, kind provider.ProviderKind) (service.Provider, error)
}

// providerConfigReader resolves the raw stored credentials for a project —
// used here solely to read the project's bot_app_id, the expected JWT
// audience. *integration.ProjectProviderStore satisfies it
// (repository.ProviderLoader / provider.ProviderLoader shape).
type providerConfigReader interface {
	LoadProvider(ctx context.Context, projectID uuid.UUID, kind string) (credentials []byte, err error)
}

// followUpper dispatches inbound responder replies.
type followUpper interface {
	Handle(ctx context.Context, cmd command.FollowUpCommand) (command.FollowUpResult, error)
}

// openIDMetadata is the subset of the Bot Framework's OpenID Connect
// discovery document Orako reads.
type openIDMetadata struct {
	JWKSURI string `json:"jwks_uri"`
}

// WebhookHandler is the Teams-specific inbound Bot Framework Activity HTTP
// handler (POST /teams/activities/{projectID}).
type WebhookHandler struct {
	registry providerLookup
	configs  providerConfigReader
	followUp followUpper
	keySet   *oidc.RemoteKeySet
	logger   *slog.Logger
}

// NewWebhookHandler builds a WebhookHandler, performing the Bot Framework
// OpenID metadata discovery once (fetching jwks_uri) so every subsequent
// request's signature verification only ever fetches the JWKS document
// itself — and only on encountering an unknown key id (oidc.RemoteKeySet's
// own caching strategy), never per request.
//
// metadataURL overrides the discovery document location; empty defaults to
// defaultMetadataURL (tests point this at a fake server).
func NewWebhookHandler(
	ctx context.Context,
	metadataURL string,
	registry providerLookup,
	configs providerConfigReader,
	followUp followUpper,
	logger *slog.Logger,
) (*WebhookHandler, error) {
	if metadataURL == "" {
		metadataURL = defaultMetadataURL
	}

	jwksURI, err := fetchJWKSURI(ctx, metadataURL)
	if err != nil {
		return nil, fmt.Errorf("teams webhook: discovering Bot Framework OpenID metadata: %w", err)
	}

	// oidc.ClientContext threads httpClient through to every JWKS fetch the
	// RemoteKeySet performs later (on Verify, when it encounters an unknown
	// key id) — without it, go-oidc falls back to the unbounded
	// http.DefaultClient.
	keySetCtx := oidc.ClientContext(ctx, httpClient)

	return &WebhookHandler{
		registry: registry,
		configs:  configs,
		followUp: followUp,
		keySet:   oidc.NewRemoteKeySet(keySetCtx, jwksURI),
		logger:   logger,
	}, nil
}

// readLimitedBody reads r.Body capped at limit bytes, so an oversized
// unauthenticated payload cannot exhaust memory. Writes the HTTP error
// response itself and returns ok=false on any read failure: 413 when the cap
// was exceeded, 400 otherwise.
func readLimitedBody(w http.ResponseWriter, r *http.Request, limit int64, logPrefix string, logger *slog.Logger) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			logger.WarnContext(r.Context(), logPrefix+": body exceeds size cap", slog.Int64("limit_bytes", limit))
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)

			return nil, false
		}

		logger.WarnContext(r.Context(), logPrefix+": cannot read body", slog.Any("error", err))
		http.Error(w, "cannot read body", http.StatusBadRequest)

		return nil, false
	}

	return raw, true
}

// fetchJWKSURI fetches and decodes the OpenID Connect discovery document at
// metadataURL, returning its jwks_uri.
func fetchJWKSURI(ctx context.Context, metadataURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return "", fmt.Errorf("building metadata request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching metadata: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body close errors are informational in defer

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("metadata endpoint returned status %d", resp.StatusCode)
	}

	var meta openIDMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", fmt.Errorf("decoding metadata: %w", err)
	}

	if meta.JWKSURI == "" {
		return "", errors.New("metadata document has no jwks_uri")
	}

	return meta.JWKSURI, nil
}

// RegisterRoutes mounts the Teams activities endpoint on the router.
// Pattern: POST /teams/activities/{projectID}.
func (h *WebhookHandler) RegisterRoutes(r chi.Router) {
	r.Post("/teams/activities/{projectID}", h.ServeActivities)
}

// ServeActivities handles a single inbound Bot Framework Activity POST: JWT
// verification (issuer + this project's bot_app_id as audience + signature +
// expiry), then the reply funnels through FollowUp — mirroring the Slack
// webhook handler's dispatch.
func (h *WebhookHandler) ServeActivities(w http.ResponseWriter, r *http.Request) {
	projectIDStr := chi.URLParam(r, "projectID")

	projectID, err := uuid.Parse(projectIDStr)
	if err != nil {
		http.Error(w, "invalid project_id in path", http.StatusBadRequest)
		return
	}

	botAppID, ok := h.resolveBotAppID(w, r, projectID)
	if !ok {
		return
	}

	rawToken, ok := bearerToken(w, r)
	if !ok {
		return
	}

	if !h.verifyToken(w, r, rawToken, botAppID) {
		return
	}

	raw, ok := readLimitedBody(w, r, maxActivityBodyBytes, "teams webhook", h.logger)
	if !ok {
		return
	}

	prov, err := h.registry.ForProjectKind(r.Context(), projectID, provider.ProviderKindTeams)
	if err != nil {
		h.logger.WarnContext(r.Context(), "teams webhook: no provider for project",
			slog.String("project_id", projectIDStr), slog.Any("error", err))
		http.Error(w, "no provider configured for project", http.StatusNotFound)

		return
	}

	h.dispatchMessage(w, r, prov, raw)
}

// resolveBotAppID reads the project's stored Teams credentials to determine
// the expected JWT audience. Reads the "teams" kind specifically — a project
// with other kinds also configured (e.g. Slack) must not have this pick up
// some other kind's credentials by recency (review fit#4). Writes the HTTP
// error response itself on failure and returns ok=false.
func (h *WebhookHandler) resolveBotAppID(w http.ResponseWriter, r *http.Request, projectID uuid.UUID) (string, bool) {
	credJSON, err := h.configs.LoadProvider(r.Context(), projectID, "teams")
	if err != nil {
		h.logger.WarnContext(r.Context(), "teams webhook: no provider config for project",
			slog.String("project_id", projectID.String()), slog.Any("error", err))
		http.Error(w, "no provider configured for project", http.StatusNotFound)

		return "", false
	}

	var creds map[string]string
	if err := json.Unmarshal(credJSON, &creds); err != nil {
		h.logger.ErrorContext(r.Context(), "teams webhook: decoding stored credentials", slog.Any("error", err))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return "", false
	}

	botAppID := creds["bot_app_id"]
	if botAppID == "" {
		http.Error(w, "project has no bot_app_id configured", http.StatusNotFound)
		return "", false
	}

	return botAppID, true
}

// bearerToken extracts the raw JWT from the Authorization header. Writes 401
// and returns ok=false on a missing or malformed header.
func bearerToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || raw == "" {
		http.Error(w, "missing or malformed Authorization header", http.StatusUnauthorized)
		return "", false
	}

	return raw, true
}

// verifyToken validates rawToken's signature (via the shared JWKS key set),
// issuer, audience (this project's bot_app_id), and expiry. Writes 401 and
// returns false on any validation failure.
func (h *WebhookHandler) verifyToken(w http.ResponseWriter, r *http.Request, rawToken, botAppID string) bool {
	verifier := oidc.NewVerifier(botFrameworkIssuer, h.keySet, &oidc.Config{
		ClientID: botAppID,
	})

	if _, err := verifier.Verify(r.Context(), rawToken); err != nil {
		h.logger.WarnContext(r.Context(), "teams webhook: JWT verification failed", slog.Any("error", err))
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return false
	}

	return true
}

// dispatchMessage parses raw as a plain reply and dispatches FollowUp.
func (h *WebhookHandler) dispatchMessage(w http.ResponseWriter, r *http.Request, prov service.Provider, raw []byte) {
	inbound, err := prov.ParseInbound(r.Context(), raw)
	if err != nil {
		if errors.Is(err, service.ErrUnrecognizedMessage) {
			// Not a managed conversation: ack 200 so Bot Framework does not retry.
			w.WriteHeader(http.StatusOK)
			return
		}

		h.logger.WarnContext(r.Context(), "teams webhook: ParseInbound failed", slog.Any("error", err))
		http.Error(w, "cannot parse activity", http.StatusBadRequest)

		return
	}

	if _, err := h.followUp.Handle(r.Context(), command.FollowUpCommand{
		ConversationID: inbound.ConversationID,
		AuthorMemberID: inbound.AuthorMemberID,
		Message:        inbound.Body,
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "teams webhook: FollowUp dispatch failed",
			slog.String("conversation_id", inbound.ConversationID.String()), slog.Any("error", err))
		http.Error(w, "internal dispatch error", http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusOK)
}
