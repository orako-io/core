// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// onboardingStore reads and writes a member's permanent Get-started dismissal.
// *identity.MemberStore satisfies it.
type onboardingStore interface {
	OnboardingDismissed(ctx context.Context, memberID uuid.UUID) (bool, error)
	SetOnboardingDismissed(ctx context.Context, memberID uuid.UUID, dismissed bool) error
}

// OnboardingHandler serves the per-member Get-started dismissal flag. It is a
// plain HTTP surface (not a Connect-RPC) so the flag can be read/written without
// a proto wire change, and it lives next to the dashboard like the license
// surface:
//
//	GET    /api/onboarding          — AUTHENTICATED; returns {"dismissed": bool}
//	POST   /api/onboarding/dismiss  — mark the onboarding dismissed for good
//	DELETE /api/onboarding/dismiss  — undo (the page + nav item come back)
//
// The dismissal is server-side and per member, so it survives reloads and
// follows the member across devices — unlike a localStorage flag.
type OnboardingHandler struct {
	store onboardingStore
	auth  Authenticator
	log   *slog.Logger
}

// NewOnboardingHandler builds the handler.
func NewOnboardingHandler(store onboardingStore, auth Authenticator, log *slog.Logger) *OnboardingHandler {
	return &OnboardingHandler{store: store, auth: auth, log: log}
}

// RegisterRoutes mounts the read + the dismiss/undo writes.
func (h *OnboardingHandler) RegisterRoutes(r chi.Router) {
	r.Get("/api/onboarding", h.getStatus)
	r.Post("/api/onboarding/dismiss", h.dismiss)
	r.Delete("/api/onboarding/dismiss", h.undismiss)
}

// caller authenticates the request and resolves the member id. It writes the
// 401 itself and returns ok=false when the caller cannot be identified.
func (h *OnboardingHandler) caller(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := h.auth.Authenticate(r.Context(), r.Header.Get("Authorization"))
	if err != nil {
		writeJoinError(w, http.StatusUnauthorized, "could not verify your session")

		return uuid.Nil, false
	}

	return id.MemberID, true
}

// getStatus returns whether the caller has dismissed the onboarding. An
// account-only caller (no member yet) is never "dismissed".
func (h *OnboardingHandler) getStatus(w http.ResponseWriter, r *http.Request) {
	memberID, ok := h.caller(w, r)
	if !ok {
		return
	}

	dismissed := false

	if memberID != uuid.Nil {
		var err error

		dismissed, err = h.store.OnboardingDismissed(r.Context(), memberID)
		if err != nil {
			// A read miss must not break the dashboard shell — default to "not
			// dismissed" (the page shows) and log the cause.
			h.log.WarnContext(r.Context(), "reading onboarding dismissal failed",
				slog.String("member_id", memberID.String()), slog.Any("error", err))

			dismissed = false
		}
	}

	writeOnboardingStatus(w, dismissed)
}

// dismiss marks the onboarding permanently dismissed for the caller.
func (h *OnboardingHandler) dismiss(w http.ResponseWriter, r *http.Request) {
	h.setDismissed(w, r, true)
}

// undismiss clears the dismissal (Undo).
func (h *OnboardingHandler) undismiss(w http.ResponseWriter, r *http.Request) {
	h.setDismissed(w, r, false)
}

func (h *OnboardingHandler) setDismissed(w http.ResponseWriter, r *http.Request, dismissed bool) {
	memberID, ok := h.caller(w, r)
	if !ok {
		return
	}

	if memberID == uuid.Nil {
		// An account with no member cannot dismiss anything (there is no member
		// row to flag). Treat as a no-op success so the SPA never errors.
		writeOnboardingStatus(w, false)

		return
	}

	if err := h.store.SetOnboardingDismissed(r.Context(), memberID, dismissed); err != nil {
		h.log.ErrorContext(r.Context(), "writing onboarding dismissal failed",
			slog.String("member_id", memberID.String()),
			slog.Bool("dismissed", dismissed),
			slog.Any("error", err),
		)
		writeJoinError(w, http.StatusInternalServerError, "could not save your preference")

		return
	}

	writeOnboardingStatus(w, dismissed)
}

// writeOnboardingStatus writes the {"dismissed": bool} body.
func writeOnboardingStatus(w http.ResponseWriter, dismissed bool) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(map[string]bool{"dismissed": dismissed})
}
