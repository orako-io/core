// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/infra/api/oauth"
	"github.com/orako-io/core/internal/pkg/errs"
)

// maxJoinBody caps the tiny {"code":"…"} payload.
const maxJoinBody = 4 << 10 // 4 KiB

// accountAuthenticator resolves a bearer to at least an account, tolerating an
// account with no member yet (MemberID nil) — exactly the brand-new joiner the
// /join page serves. HumanAuthenticatorAdapter satisfies it via
// AuthenticateAccount, the member-optional login seam.
type accountAuthenticator interface {
	AuthenticateAccount(ctx context.Context, bearer string) (oauth.AccountIdentity, error)
}

// joinCodeRedeemer provisions the account as a pending member of the code's
// org/project. *command.RedeemJoinTokenHandler satisfies it.
type joinCodeRedeemer interface {
	Handle(ctx context.Context, cmd command.RedeemJoinTokenCommand) (command.RedeemJoinTokenResult, error)
}

// joinProjectLocator resolves the redeemed project's owning org, so the org
// name can be revealed AFTER a successful redeem. *identity.ProjectStore
// satisfies it.
type joinProjectLocator interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Project, error)
}

// joinOrgReader reads an org's public identity (id + name). *query.GetOrganizationHandler satisfies it.
type joinOrgReader interface {
	Handle(ctx context.Context, q query.GetOrganizationQuery) (query.OrgIdentityView, error)
}

// joinTokenReader resolves a raw join code to its org/project and revoked flag,
// without redeeming it. *identity.JoinTokenStore satisfies it. Used by the public
// GET /join/{code}/org lookup to name the org for a valid, non-revoked code.
type joinTokenReader interface {
	JoinTokenByToken(ctx context.Context, token string) (repository.JoinToken, error)
}

// JoinHandler serves the self-serve join-code redemption endpoint that backs the
// dedicated /join welcome page. It is deliberately a plain HTTP endpoint, NOT a
// member-gated Connect-RPC: a brand-new joiner is an authenticated ACCOUNT with
// no member yet, so it can never pass the dashboard interceptor's member gate.
type JoinHandler struct {
	auth     accountAuthenticator
	redeem   joinCodeRedeemer
	projects joinProjectLocator
	orgs     joinOrgReader
	tokens   joinTokenReader
	log      *slog.Logger
}

// NewJoinHandler builds the handler over the member-optional account resolver,
// the redeem command handler, the org/project readers used to name the org on
// success, and the token reader backing the public org-name lookup.
func NewJoinHandler(auth accountAuthenticator, redeem joinCodeRedeemer, projects joinProjectLocator, orgs joinOrgReader, tokens joinTokenReader, log *slog.Logger) *JoinHandler {
	return &JoinHandler{auth: auth, redeem: redeem, projects: projects, orgs: orgs, tokens: tokens, log: log}
}

// RegisterRoutes mounts POST /join and the public GET /join/{code}/org lookup.
func (h *JoinHandler) RegisterRoutes(r chi.Router) {
	r.Post("/join", h.postJoin)
	r.Get("/join/{code}/org", h.getJoinOrg)
}

// getJoinOrg reveals ONLY the org name behind a valid, non-revoked join code so
// the invite-aware auth page can greet a prospective joiner by org before they
// sign in. Deliberately UNauthenticated: the joiner has no session yet. A bad,
// unknown, or revoked code returns a detail-free 404 — a guesser with a wrong
// code learns nothing (codes are 192-bit high-entropy), and the org name is only
// ever exposed for a code someone already legitimately holds.
func (h *JoinHandler) getJoinOrg(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(chi.URLParam(r, "code"))
	if code == "" {
		writeJoinError(w, http.StatusNotFound, "not found")

		return
	}

	tok, err := h.tokens.JoinTokenByToken(r.Context(), code)
	if err != nil || tok.Revoked || tok.OrgID == uuid.Nil {
		writeJoinError(w, http.StatusNotFound, "not found")

		return
	}

	org, err := h.orgs.Handle(r.Context(), query.GetOrganizationQuery{OrgID: tok.OrgID})
	if err != nil || strings.TrimSpace(org.Name) == "" {
		writeJoinError(w, http.StatusNotFound, "not found")

		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"orgName": org.Name})
}

// joinResponse is the success body. orgName is populated ONLY on a successful
// redeem — never before, and never for an invalid/revoked code — so a stranger
// probing codes can never learn which org a guessed code belongs to.
type joinResponse struct {
	OrgName       string `json:"orgName"`
	AlreadyMember bool   `json:"alreadyMember"`
}

// postJoin authenticates the caller as an account (member-optional), redeems the
// code, and — only on success — reveals the org name.
func (h *JoinHandler) postJoin(w http.ResponseWriter, r *http.Request) {
	bearer, ok := joinBearerFromHeader(r)
	if !ok {
		writeJoinError(w, http.StatusUnauthorized, "sign in to join")

		return
	}

	var body struct {
		Code string `json:"code"`
	}

	if err := json.NewDecoder(io.LimitReader(r.Body, maxJoinBody)).Decode(&body); err != nil {
		writeJoinError(w, http.StatusBadRequest, "invalid request body")

		return
	}

	code := strings.TrimSpace(body.Code)
	if code == "" {
		writeJoinError(w, http.StatusBadRequest, "a join code is required")

		return
	}

	// Member-optional: resolves (and, on first login, auto-creates) the account
	// behind the dashboard session even when it has joined no project yet.
	acct, err := h.auth.AuthenticateAccount(r.Context(), bearer)
	if err != nil {
		// Log the real cause: the 401 body stays generic (no client leak), but the
		// underlying verify/resolve error is what we need to diagnose a failing join.
		h.log.WarnContext(r.Context(), "join redeem: account authentication failed", slog.Any("error", err))
		writeJoinError(w, http.StatusUnauthorized, "could not verify your session")

		return
	}

	res, err := h.redeem.Handle(r.Context(), command.RedeemJoinTokenCommand{
		AccountID:   acct.AccountID,
		Token:       code,
		Email:       acct.Email,
		DisplayName: acct.DisplayName,
	})
	if err != nil {
		h.writeRedeemError(w, err)

		return
	}

	// Reveal the org name only now — the redeem has succeeded, so the caller is
	// entitled to see which org they just joined.
	resp := joinResponse{OrgName: h.resolveOrgName(r.Context(), res.ProjectID), AlreadyMember: res.AlreadyMember}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// writeRedeemError maps the detail-free redeem sentinels to status codes without
// ever leaking org/project detail: invalid → 400, revoked → 403, a malformed
// input → 400, anything else → 500.
func (h *JoinHandler) writeRedeemError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, command.ErrJoinTokenInvalid):
		writeJoinError(w, http.StatusBadRequest, command.ErrJoinTokenInvalid.Error())
	case errors.Is(err, command.ErrJoinTokenRevoked):
		writeJoinError(w, http.StatusForbidden, command.ErrJoinTokenRevoked.Error())
	default:
		var inv errs.InvalidError
		if errors.As(err, &inv) {
			// Never echo which field/reason — keep the join surface detail-free.
			writeJoinError(w, http.StatusBadRequest, command.ErrJoinTokenInvalid.Error())

			return
		}

		h.log.Error("join code redemption failed", slog.String("error", err.Error()))
		writeJoinError(w, http.StatusInternalServerError, "internal error")
	}
}

// resolveOrgName best-effort resolves the org name from the redeemed project.
// The redemption already succeeded, so a lookup miss degrades to an empty name
// rather than failing the whole request.
func (h *JoinHandler) resolveOrgName(ctx context.Context, projectID uuid.UUID) string {
	project, err := h.projects.ByID(ctx, projectID)
	if err != nil || project.OrgID == uuid.Nil {
		return ""
	}

	org, err := h.orgs.Handle(ctx, query.GetOrganizationQuery{OrgID: project.OrgID})
	if err != nil {
		return ""
	}

	return org.Name
}

// writeJoinError writes a small JSON error body. The message is intentionally
// generic for the auth/invalid/revoked cases (no org/project detail).
func writeJoinError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// joinBearerFromHeader extracts the raw bearer token from a standard
// "Authorization: Bearer <token>" header. AuthenticateAccount re-adds the
// prefix itself, so the raw token (no prefix) is returned.
func joinBearerFromHeader(r *http.Request) (string, bool) {
	const prefix = "Bearer "

	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}

	token := strings.TrimSpace(h[len(prefix):])
	if token == "" {
		return "", false
	}

	return token, true
}
