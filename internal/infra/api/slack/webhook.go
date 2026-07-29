// SPDX-License-Identifier: AGPL-3.0-or-later

package slack

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/provider"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/service"
)

// signingSecretResolver loads a project's stored Slack credentials so the
// webhook can verify signatures with THAT workspace's signing secret — every
// org brings its own Slack app (manual/manifest setup), so a single
// server-level secret cannot verify them all.
// *eventlog.OrgResolvingProviderLoader satisfies it.
type signingSecretResolver interface {
	LoadProvider(ctx context.Context, projectID uuid.UUID, kind string) ([]byte, error)
}

// Slack-specific HTTP header names.
const (
	headerSlackSignature = "X-Slack-Signature"
	headerSlackTimestamp = "X-Slack-Request-Timestamp"
)

// maxWebhookBodyBytes caps the request body an unauthenticated Slack webhook
// (Events API or interactivity) will read before signature verification, so
// an oversized payload cannot exhaust memory ahead of that check. 1 MiB
// comfortably covers Slack's documented payload sizes.
const maxWebhookBodyBytes = 1 << 20 // 1 MiB

// readLimitedBody reads r.Body capped at maxWebhookBodyBytes. Writes the HTTP
// error response itself and returns ok=false on any read failure: 413 when
// the cap was exceeded, 400 otherwise. logPrefix names the caller in the log
// line (e.g. "slack webhook", "slack interactivity").
func readLimitedBody(w http.ResponseWriter, r *http.Request, logPrefix string, logger *slog.Logger) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			logger.WarnContext(r.Context(), logPrefix+": body exceeds size cap", slog.Int64("limit_bytes", maxWebhookBodyBytes))
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)

			return nil, false
		}

		logger.WarnContext(r.Context(), logPrefix+": cannot read body", slog.Any("error", err))
		http.Error(w, "cannot read body", http.StatusBadRequest)

		return nil, false
	}

	return raw, true
}

// providerLookup resolves the project's Slack provider from the registry —
// this route is Slack-specific, so the kind is always
// provider.ProviderKindSlack, never "whichever provider the project has"
// (review fit#4: a project may also have Teams/Discord/Telegram configured).
type providerLookup interface {
	ForProjectKind(ctx context.Context, projectID uuid.UUID, kind provider.ProviderKind) (service.Provider, error)
}

// followUpper dispatches inbound responder replies to the application layer.
type followUpper interface {
	Handle(ctx context.Context, cmd command.FollowUpCommand) (command.FollowUpResult, error)
}

// urlVerificationPayload is the envelope Slack sends to verify a new Events
// API endpoint; Orako must echo the challenge back.
type urlVerificationPayload struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
}

// WebhookHandler is the Slack-specific inbound Events API HTTP handler
// (POST /slack/events/{projectID}). It wraps the generic ParseInbound →
// FollowUp flow with v0 HMAC-SHA256 signature verification, the
// url_verification handshake, and event_id deduplication.
type WebhookHandler struct {
	secrets signingSecretResolver
	// fallbackSecret is the legacy server-level signing secret
	// (ORAKO_SLACK_SIGNING_SECRET); used only when the project has no stored
	// signing_secret. Empty is fine.
	fallbackSecret string
	registry       providerLookup
	followUp       followUpper
	logger         *slog.Logger

	// seenEvents deduplicates Slack event_ids (Slack retries on non-2xx). Values
	// are the first-seen time so entries older than slackDedupTTL are evicted —
	// the map stays bounded (L10). In-process only; multi-instance needs Redis/DB.
	mu         sync.Mutex `exhaustruct:"optional"`
	seenEvents map[string]time.Time
}

// slackDedupTTL bounds how long an event_id is remembered for dedup — well past
// Slack's retry window, but short enough that the map can't grow unbounded.
const slackDedupTTL = time.Hour

// NewWebhookHandler builds a WebhookHandler. secrets resolves each project's
// own signing secret from its stored credentials; fallbackSecret (optional)
// covers legacy deployments that configured one via the environment.
func NewWebhookHandler(
	secrets signingSecretResolver,
	fallbackSecret string,
	registry providerLookup,
	followUp followUpper,
	logger *slog.Logger,
) *WebhookHandler {
	return &WebhookHandler{
		secrets:        secrets,
		fallbackSecret: fallbackSecret,
		registry:       registry,
		followUp:       followUp,
		logger:         logger,
		seenEvents:     make(map[string]time.Time),
	}
}

// RegisterRoutes mounts the Slack Events API endpoint on the router.
// Pattern: POST /slack/events/{projectID}.
func (h *WebhookHandler) RegisterRoutes(r chi.Router) {
	r.Post("/slack/events/{projectID}", h.ServeEvents)
}

// ServeEvents handles a single inbound Slack Events API POST. The signature is
// verified against the raw body BEFORE any payload parsing, with the signing
// secret resolved from the PROJECT in the path (each org brings its own Slack
// app; only the path is read pre-verification, never the payload).
func (h *WebhookHandler) ServeEvents(w http.ResponseWriter, r *http.Request) {
	raw, ok := readLimitedBody(w, r, "slack webhook", h.logger)
	if !ok {
		return
	}

	projectIDStr := chi.URLParam(r, "projectID")

	projectID, err := uuid.Parse(projectIDStr)
	if err != nil {
		http.Error(w, "invalid project_id in path", http.StatusBadRequest)
		return
	}

	secret := h.signingSecretFor(r.Context(), projectID)
	if secret == "" {
		h.logger.WarnContext(r.Context(), "slack webhook: no signing secret configured for project",
			slog.String("project_id", projectIDStr))
		http.Error(w, "slack is not configured for this project", http.StatusNotFound)

		return
	}

	if err := NewVerifier(secret).Verify(
		r.Header.Get(headerSlackSignature),
		r.Header.Get(headerSlackTimestamp),
		raw,
	); err != nil {
		h.logger.WarnContext(r.Context(), "slack webhook: signature rejected",
			slog.String("reason", err.Error()), // error text is safe; no secret material
		)
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	if handled := h.maybeHandleChallenge(w, raw); handled {
		return
	}

	prov, err := h.registry.ForProjectKind(r.Context(), projectID, provider.ProviderKindSlack)
	if err != nil {
		h.logger.WarnContext(r.Context(), "slack webhook: no provider for project",
			slog.String("project_id", projectIDStr),
			slog.Any("error", err),
		)
		http.Error(w, "no provider configured for project", http.StatusNotFound)

		return
	}

	inbound, err := prov.ParseInbound(r.Context(), raw)
	if err != nil {
		if errors.Is(err, service.ErrUnrecognizedMessage) {
			// Not a managed conversation: ack 200 so Slack does not retry.
			w.WriteHeader(http.StatusOK)
			return
		}

		h.logger.WarnContext(r.Context(), "slack webhook: ParseInbound failed",
			slog.String("project_id", projectIDStr),
			slog.Any("error", err),
		)
		http.Error(w, "cannot parse event", http.StatusBadRequest)

		return
	}

	if eventID := extractEventID(raw); eventID != "" && h.alreadySeen(eventID) {
		h.logger.InfoContext(r.Context(), "slack webhook: duplicate event_id, skipping",
			slog.String("event_id", eventID),
		)
		w.WriteHeader(http.StatusOK)

		return
	}

	h.dispatchFollowUp(w, r, inbound)
}

// alreadySeen records eventID and reports whether it was seen before (Slack
// retries on non-2xx). It evicts entries older than slackDedupTTL on each call
// so the map stays bounded (L10). Split out of ServeEvents to keep its
// cyclomatic complexity down.
func (h *WebhookHandler) alreadySeen(eventID string) bool {
	now := time.Now()

	h.mu.Lock()
	defer h.mu.Unlock()

	for id, seen := range h.seenEvents {
		if now.Sub(seen) > slackDedupTTL {
			delete(h.seenEvents, id)
		}
	}

	if _, already := h.seenEvents[eventID]; already {
		return true
	}

	h.seenEvents[eventID] = now

	return false
}

// dispatchFollowUp runs the FollowUp command and writes the HTTP response:
// ack 200 on an append, 500 on a real dispatch failure. A reply is a message
// (hub-and-spoke phase 2) — there is no claim rejection or second-opinion arm
// to translate anymore. Split out of ServeEvents to keep its complexity down.
func (h *WebhookHandler) dispatchFollowUp(
	w http.ResponseWriter,
	r *http.Request,
	inbound service.InboundMessage,
) {
	// Slack requires a response within 3 seconds; the dispatch path is fast.
	if _, err := h.followUp.Handle(r.Context(), command.FollowUpCommand{
		ConversationID: inbound.ConversationID,
		AuthorMemberID: inbound.AuthorMemberID,
		Message:        inbound.Body,
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "slack webhook: FollowUp dispatch failed",
			slog.String("conversation_id", inbound.ConversationID.String()),
			slog.Any("error", err),
		)
		http.Error(w, "internal dispatch error", http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusOK)
}

// signingSecretFor resolves the signing secret for a project: the stored
// credentials' signing_secret first, the legacy server-level fallback second,
// empty when neither exists.
func (h *WebhookHandler) signingSecretFor(ctx context.Context, projectID uuid.UUID) string {
	if h.secrets != nil {
		if raw, err := h.secrets.LoadProvider(ctx, projectID, "slack"); err == nil {
			var creds struct {
				SigningSecret string `json:"signing_secret"`
			}

			if err := json.Unmarshal(raw, &creds); err == nil && creds.SigningSecret != "" {
				return creds.SigningSecret
			}
		}
	}

	return h.fallbackSecret
}

// maybeHandleChallenge detects and responds to a Slack url_verification request.
// Returns true if the payload was a challenge (and the response has been written).
func (h *WebhookHandler) maybeHandleChallenge(w http.ResponseWriter, raw []byte) bool {
	var env urlVerificationPayload
	if err := json.Unmarshal(raw, &env); err != nil {
		return false
	}

	if env.Type != "url_verification" {
		return false
	}

	resp, _ := json.Marshal(map[string]string{"challenge": env.Challenge})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resp)

	return true
}

type slackEventEnvelope struct {
	EventID string `json:"event_id"`
}

// extractEventID parses only the event_id from the outer Slack payload;
// empty when absent or on parse failure.
func extractEventID(raw []byte) string {
	var env slackEventEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return ""
	}

	return env.EventID
}
