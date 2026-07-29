// SPDX-License-Identifier: AGPL-3.0-or-later

// Package api — generic inbound provider webhook
// (POST /webhook/{provider}/{projectID}) for Teams/Telegram. Slack MUST use
// the dedicated /slack/events/{projectID} handler, which verifies signatures.
package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/provider"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/service"
)

// telegramSecretHeader is the header Telegram echoes on every webhook update,
// carrying the secret_token given to setWebhook. It is the only authenticity
// signal on the generic inbound route.
const telegramSecretHeader = "X-Telegram-Bot-Api-Secret-Token" //nolint:gosec // header name, not a credential

// providerLookup is the narrow interface for resolving a Provider by its
// exact (project, kind) pair — this generic route serves several kinds
// (Telegram, Discord — Slack and Teams have their own dedicated routes), so
// it must resolve by the {provider} path segment's kind, never "the
// project's provider" (review fit#4: a project can have more than one).
type providerLookup interface {
	ForProjectKind(ctx context.Context, projectID uuid.UUID, kind provider.ProviderKind) (service.Provider, error)
}

// attachmentIngestor downloads, stores, and records a human reply's inbound
// attachments, returning ids to link onto the follow-up. *inbound.Ingestor
// satisfies it. Optional (nil = inbound attachments off, text still delivered).
type attachmentIngestor interface {
	Ingest(ctx context.Context, conversationID, authorMemberID uuid.UUID, atts []service.InboundAttachment) []uuid.UUID
}

// WebhookHandler handles inbound provider webhooks and dispatches them to the
// application's FollowUp command handler.
type WebhookHandler struct {
	registry       providerLookup
	followUp       followUpper
	telegramSecret string
	ingestor       attachmentIngestor
	logger         *slog.Logger
}

// NewWebhookHandler builds a WebhookHandler. telegramSecret authenticates the
// Telegram inbound route; when empty, Telegram inbound is rejected outright
// (an unsigned payload is never trusted). ingestor may be nil (inbound
// attachments are dropped, text still delivered).
func NewWebhookHandler(
	registry providerLookup,
	followUp followUpper,
	telegramSecret string,
	ingestor attachmentIngestor,
	logger *slog.Logger,
) *WebhookHandler {
	return &WebhookHandler{
		registry:       registry,
		followUp:       followUp,
		telegramSecret: telegramSecret,
		ingestor:       ingestor,
		logger:         logger,
	}
}

// RegisterRoutes mounts the webhook endpoint on the router.
// Pattern: POST /webhook/{provider}/{projectID}.
func (h *WebhookHandler) RegisterRoutes(r chi.Router) {
	r.Post("/webhook/{provider}/{projectID}", h.ServeWebhook)
}

// ServeWebhook handles a single inbound provider webhook POST.
func (h *WebhookHandler) ServeWebhook(w http.ResponseWriter, r *http.Request) {
	providerStr := chi.URLParam(r, "provider")
	if providerStr == "teams" {
		// Teams now has its own JWT-verified route; this generic route never
		// verified inbound requests, so it must not accept teams traffic
		// anymore. 410 (not 404): the route existed and is intentionally gone,
		// not a typo'd path.
		http.Error(w, "teams now uses POST /teams/activities/{projectID} (JWT-verified)", http.StatusGone)
		return
	}

	// Every remaining kind on this generic route (Telegram; a Discord POST only
	// ever acks) is authenticated by the Telegram secret_token header. Verified
	// BEFORE resolving the provider or reading the body, so an unauthenticated
	// caller cannot forge an attributed reply or force work. Fail closed when no
	// secret is configured: the route then serves nobody.
	if !h.verifyTelegramSecret(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	projectIDStr := chi.URLParam(r, "projectID")

	projectID, err := uuid.Parse(projectIDStr)
	if err != nil {
		http.Error(w, "invalid project_id in path", http.StatusBadRequest)
		return
	}

	prov, err := h.registry.ForProjectKind(r.Context(), projectID, provider.ProviderKind(providerStr))
	if err != nil {
		h.logger.WarnContext(r.Context(), "webhook: no provider for project",
			slog.String("project_id", projectIDStr),
			slog.String("provider", providerStr),
			slog.Any("error", err),
		)
		http.Error(w, "no provider configured for project", http.StatusNotFound)

		return
	}

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "cannot read request body", http.StatusBadRequest)
		return
	}

	inbound, err := prov.ParseInbound(r.Context(), raw)
	if err != nil {
		if errors.Is(err, service.ErrUnrecognizedMessage) {
			// Not a managed conversation: ack 200 so providers do not retry.
			w.WriteHeader(http.StatusOK)
			return
		}

		h.logger.WarnContext(r.Context(), "webhook: ParseInbound failed",
			slog.String("project_id", projectIDStr),
			slog.Any("error", err),
		)
		http.Error(w, "cannot parse provider payload", http.StatusBadRequest)

		return
	}

	// Inbound attachments (a human replied with a photo/document) are ingested
	// best-effort: each is downloaded from its token-bearing FetchURL, stored,
	// and recorded as an unlinked row, then linked to the reply by FollowUp. A
	// failed attachment never blocks the text reply.
	var attachmentIDs []uuid.UUID
	if h.ingestor != nil {
		attachmentIDs = h.ingestor.Ingest(r.Context(), inbound.ConversationID, inbound.AuthorMemberID, inbound.Attachments)
	}

	// All inbound replies append via FollowUp; ResolveConversation is always
	// agent-driven via RPC, never triggered by a webhook.
	if _, err = h.followUp.Handle(r.Context(), command.FollowUpCommand{
		ConversationID: inbound.ConversationID,
		AuthorMemberID: inbound.AuthorMemberID,
		Message:        inbound.Body,
		AttachmentIDs:  attachmentIDs,
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "webhook: FollowUp dispatch failed",
			slog.String("conversation_id", inbound.ConversationID.String()),
			slog.Any("error", err),
		)
		http.Error(w, "internal dispatch error", http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusOK)
}

// verifyTelegramSecret constant-time-compares the request's secret_token header
// against the configured secret. Returns false when no secret is configured
// (fail closed) or the header does not match.
func (h *WebhookHandler) verifyTelegramSecret(r *http.Request) bool {
	if h.telegramSecret == "" {
		return false
	}

	got := r.Header.Get(telegramSecretHeader)

	return subtle.ConstantTimeCompare([]byte(got), []byte(h.telegramSecret)) == 1
}
