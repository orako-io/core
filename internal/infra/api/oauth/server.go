// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// MemberIdentity is the human the /authorize consent screen was shown to and
// approved as — the subject every issued authorization code and token is
// bound to. Deliberately narrower than transport.CallerIdentity (no
// project/role/org-admin): those are resolved fresh from Postgres on every
// resource-server call, never cached in the code or token row.
type MemberIdentity struct {
	MemberID uuid.UUID
	// Email is best-effort, shown on the consent screen; may be empty.
	Email string `exhaustruct:"optional"`
}

// AccountIdentity is the member-optional identity used by the standalone
// `/join/:code` flow. MemberID is nil until the account joins a project.
type AccountIdentity struct {
	AccountID uuid.UUID
	// MemberID is the account's existing member, or the nil UUID when it has not
	// joined a project yet.
	MemberID uuid.UUID `exhaustruct:"optional"`
	// Email / DisplayName carry the human's identity from the account for `/join`.
	Email       string `exhaustruct:"optional"`
	DisplayName string `exhaustruct:"optional"`
}

// HumanAuthenticator verifies the bearer proof of login presented at
// /authorize and resolves the member it belongs to. Implementations reuse the
// existing ORAKO_AUTH_MODE seam (auth.TokenVerifier + PrincipalResolver /
// transport.Authenticator) — this package never builds a second identity
// system.
type HumanAuthenticator interface {
	// Authenticate resolves the bearer to a member, rejecting an account with no
	// member yet — the normal (already-joined) consent path.
	Authenticate(ctx context.Context, bearer string) (MemberIdentity, error)
	// AuthenticateForOrg resolves the logged-in account to its live member row
	// inside orgID. Multi-org accounts have one member row per organization, so
	// the consent screen must bind the token to the explicitly selected org.
	AuthenticateForOrg(ctx context.Context, bearer string, orgID uuid.UUID) (MemberIdentity, error)
}

// Server is Orako's thin MCP Authorization Server: metadata + PRM discovery,
// dynamic client registration, and the authorize/token pair. One value wires
// every route in RegisterRoutes.
type Server struct {
	baseURL string
	store   *Store
	humans  HumanAuthenticator
	// unsupportedMessage, when non-empty, is the sanctioned dev-mode posture:
	// ORAKO_AUTH_MODE=dev has no real upstream login to reuse, so the
	// dashboard SPA's /authorize page renders this message client-side
	// (isOidc check) instead of the consent screen, and
	// ServeAuthorizeApprove rejects the authenticated approve call with it
	// too (defense in depth) rather than faking an identity or crashing.
	unsupportedMessage string
	now                func() time.Time
}

// NewServer builds a Server. humans authenticates the browser's dashboard
// session at POST /oauth/authorize/approve; pass unsupportedMessage
// non-empty to disable remote MCP OAuth entirely (dev mode) and have both the
// SPA and the approve endpoint report it instead.
func NewServer(baseURL string, store *Store, humans HumanAuthenticator, unsupportedMessage string) *Server {
	return &Server{
		baseURL:            baseURL,
		store:              store,
		humans:             humans,
		unsupportedMessage: unsupportedMessage,
		now:                time.Now,
	}
}

// ResourceURL is the canonical RFC 8707 resource identifier for Orako's
// single MCP endpoint — the audience every issued token is bound to and the
// resource server checks for an exact match.
func (s *Server) ResourceURL() string {
	return s.baseURL + "/mcp"
}

// RegisterRoutes mounts every AS endpoint on r.
//
// GET /authorize is deliberately NOT registered here (phase 5): the
// dashboard SPA now owns that route (AuthorizePage.tsx, behind RequireToken)
// so the browser's existing Supabase/dev session authenticates the human
// instead of a paste-your-bearer form. An unmatched GET /authorize falls
// through to the embedded SPA's catch-all (cmd/orako-server/main.go's
// r.Handle("/*", webui.Handler()), mounted after this call) exactly like
// every other client-side route.
func (s *Server) RegisterRoutes(r chi.Router) {
	r.Get("/.well-known/oauth-authorization-server", s.ServeASMetadata)
	r.Get("/.well-known/oauth-protected-resource", s.ServePRM)
	r.Post("/register", s.ServeRegister)
	r.Get("/oauth/authorize/client", s.ServeAuthorizeClientInfo)
	r.Post("/oauth/authorize/approve", s.ServeAuthorizeApprove)
	r.Post("/token", s.ServeToken)
}
