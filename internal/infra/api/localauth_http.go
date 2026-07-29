// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/orako-io/core/internal/pkg/errs"
)

// maxAuthBody caps a login/register payload (tiny by nature).
const maxAuthBody = 4 << 10 // 4 KiB

// localLoginService verifies an email+password and returns a session token.
// *command.LoginHandler satisfies it. The handler stays decoupled from the
// application layer through this port.
type localLoginService interface {
	Login(ctx context.Context, email, password string) (string, error)
}

// localAcceptService turns an invite token + chosen password into a local
// account. *command.AcceptInviteHandler satisfies it.
type localAcceptService interface {
	AcceptInvite(ctx context.Context, token, password string) error
}

// localResetService drives the forgot-password flow. *command.ResetHandler
// satisfies it. RequestReset is fire-and-forget (uniform against enumeration);
// PerformReset writes the new password.
type localResetService interface {
	RequestReset(ctx context.Context, email string)
	PerformReset(ctx context.Context, token, password string) error
}

// LocalAuthHandler serves the self-host email+password endpoints, mounted only in
// auth mode "local". SaaS authenticates via Supabase OIDC and does not mount it.
type LocalAuthHandler struct {
	login  localLoginService
	accept localAcceptService
	reset  localResetService
}

// NewLocalAuthHandler builds the handler over the login, accept-invite, and
// password-reset services.
func NewLocalAuthHandler(login localLoginService, accept localAcceptService, reset localResetService) *LocalAuthHandler {
	return &LocalAuthHandler{login: login, accept: accept, reset: reset}
}

// RegisterRoutes mounts the local-auth endpoints.
func (h *LocalAuthHandler) RegisterRoutes(r chi.Router) {
	r.Post("/auth/login", h.postLogin)
	r.Post("/auth/accept-invite", h.postAcceptInvite)
	r.Post("/auth/forgot-password", h.postForgotPassword)
	r.Post("/auth/reset-password", h.postResetPassword)
}

// postForgotPassword triggers a reset email. It always answers 204 — whether or
// not the email has an account — so the response cannot probe which emails exist.
func (h *LocalAuthHandler) postForgotPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}

	if err := json.NewDecoder(io.LimitReader(r.Body, maxAuthBody)).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)

		return
	}

	h.reset.RequestReset(r.Context(), body.Email)

	w.WriteHeader(http.StatusNoContent)
}

// postResetPassword spends a reset token to set a new password. A bad/expired
// token or weak password is a 400; anything else a 500.
func (h *LocalAuthHandler) postResetPassword(w http.ResponseWriter, r *http.Request) {
	h.handleTokenPassword(w, r, h.reset.PerformReset)
}

// postAcceptInvite creates a local account from an invite token + password. A bad
// invite or a weak password is a 400; anything else a 500.
func (h *LocalAuthHandler) postAcceptInvite(w http.ResponseWriter, r *http.Request) {
	h.handleTokenPassword(w, r, h.accept.AcceptInvite)
}

// handleTokenPassword decodes a {token, password} body, runs action, and maps the
// result: InvalidError → 400 with its reason, any other error → 500, success →
// 204. Shared by accept-invite and reset-password (identical shape).
func (h *LocalAuthHandler) handleTokenPassword(
	w http.ResponseWriter,
	r *http.Request,
	action func(ctx context.Context, token, password string) error,
) {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(io.LimitReader(r.Body, maxAuthBody)).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)

		return
	}

	if err := action(r.Context(), body.Token, body.Password); err != nil {
		var inv errs.InvalidError
		if errors.As(err, &inv) {
			http.Error(w, inv.Reason, http.StatusBadRequest)

			return
		}

		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// postLogin exchanges email+password for a session token. A bad credential is a
// uniform 401 (no email probing); anything else is a 500.
func (h *LocalAuthHandler) postLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(io.LimitReader(r.Body, maxAuthBody)).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)

		return
	}

	token, err := h.login.Login(r.Context(), body.Email, body.Password)
	if err != nil {
		// Login returns errs.InvalidError only for bad credentials.
		var inv errs.InvalidError
		if errors.As(err, &inv) {
			http.Error(w, "invalid email or password", http.StatusUnauthorized)

			return
		}

		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
}
