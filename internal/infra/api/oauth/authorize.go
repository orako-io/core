// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// authorizeParams is the /authorize request shape, shared by the public
// client-info lookup and the authenticated approve endpoint.
type authorizeParams struct {
	ClientID    string
	RedirectURI string
	State       string
	Challenge   string
	Method      string
	Resource    string
}

// validateAuthorize checks client_id, the exact registered redirect_uri, PKCE
// presence/method, and defaults the RFC 8707 resource to this server's single
// hosted MCP resource when the caller omits it (locked by the phase-1 spike:
// real Claude Code never sent one, and the spec makes it optional-per-request
// for a single-resource server). A validation failure here must never react
// to the request as if the redirect_uri (or the client behind it) were
// trusted — both ServeAuthorizeClientInfo and ServeAuthorizeApprove call this
// before anything else, deny decisions included.
func (s *Server) validateAuthorize(ctx context.Context, p authorizeParams) (Client, string, error) {
	if p.ClientID == "" {
		return Client{}, "", errors.New("client_id is required")
	}

	if p.RedirectURI == "" {
		return Client{}, "", errors.New("redirect_uri is required")
	}

	client, err := s.store.GetClient(ctx, p.ClientID)
	if err != nil {
		return Client{}, "", fmt.Errorf("unknown client_id %q", p.ClientID)
	}

	if !client.HasRedirectURI(p.RedirectURI) {
		return Client{}, "", errors.New("redirect_uri is not registered for this client")
	}

	if p.Method != PKCEMethodS256 {
		return Client{}, "", errors.New("only code_challenge_method=S256 is supported")
	}

	if p.Challenge == "" {
		return Client{}, "", errors.New("code_challenge is required (PKCE)")
	}

	resource := p.Resource
	if resource == "" {
		resource = s.ResourceURL()
	}

	return client, resource, nil
}

// paramsFromQuery reads the authorizeParams shape from a GET request's query
// string — shared by ServeAuthorizeClientInfo.
func paramsFromQuery(r *http.Request) authorizeParams {
	q := r.URL.Query()

	return authorizeParams{
		ClientID:    q.Get("client_id"),
		RedirectURI: q.Get("redirect_uri"),
		State:       q.Get("state"),
		Challenge:   q.Get("code_challenge"),
		Method:      q.Get("code_challenge_method"),
		Resource:    q.Get("resource"),
	}
}

// ServeAuthorizeClientInfo is the public (unauthenticated) GET endpoint the
// dashboard SPA's /authorize consent page calls before it ever shows the
// Approve/Deny buttons: it re-runs validateAuthorize against the OAuth query
// params and, on success, returns the client's display name — exactly the
// information phase-2's server-rendered consent page showed to an
// unauthenticated visitor, just as JSON instead of HTML. A validation
// failure lets the SPA render "this request is not valid" without ever
// attempting the authenticated approve call, and without ever needing a
// second identity system or a duplicated check.
func (s *Server) ServeAuthorizeClientInfo(w http.ResponseWriter, r *http.Request) {
	client, resource, err := s.validateAuthorize(r.Context(), paramsFromQuery(r))
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"client_id":   client.ID,
		"client_name": client.Name,
		"resource":    resource,
	})
}

// approveRequest is the JSON body the dashboard SPA's AuthorizePage posts to
// the authenticated POST /oauth/authorize/approve endpoint — the seamless
// replacement for phase-2's paste-a-bearer HTML form. The browser's own
// dashboard session (Supabase JWT / dev token, attached as a normal
// Authorization header by the SPA's fetch client — see
// web/src/lib/client.ts and AuthorizePage.tsx) authenticates the human; this
// endpoint never builds a second identity system.
type approveRequest struct {
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	State               string `json:"state"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Resource            string `json:"resource"`
	Decision            string `json:"decision"`
	// OrgID is the organization explicitly selected at consent. It determines
	// which of a multi-org account's member rows owns the authorization code.
	OrgID string `json:"org_id"`
	// ProjectIDs is the projects the user ticked at consent (ordered, first =
	// primary). Empty = all the caller's projects. Membership is enforced by
	// the per-call intersection at resource-server auth, not here.
	ProjectIDs []string `json:"project_ids"`
}

// ServeAuthorizeApprove re-validates the OAuth params (validateAuthorize,
// unchanged and never duplicated), then requires the same browser session
// every other dashboard endpoint trusts before reacting to either consent
// decision. "deny" builds the access_denied redirect; "approve" authenticates
// the human and mints an authorization code exactly like phase-2's
// ServeAuthorizePost did. Both branches return the target as JSON
// ({"redirect": "..."}) rather than an HTTP 302 — the caller is the SPA's
// fetch client, which performs the actual browser navigation via
// window.location.assign once it has the URL.
//
// Validation runs before authentication, and authentication runs before
// honouring "deny": an invalid client_id/redirect_uri/PKCE/resource, or a
// request with no valid session at all, is rejected before the redirect_uri
// is ever trusted enough to react to — the same invariant validateAuthorize's
// doc comment states, now covering deny too (phase-2's HTML form only
// enforced this for approve, since the deny branch never called the
// human authenticator; JSON API version authenticates unconditionally to
// keep a single, simpler invariant instead of two subtly different ones).
func (s *Server) ServeAuthorizeApprove(w http.ResponseWriter, r *http.Request) {
	var req approveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body: "+err.Error())

		return
	}

	params := authorizeParams{
		ClientID:    req.ClientID,
		RedirectURI: req.RedirectURI,
		State:       req.State,
		Challenge:   req.CodeChallenge,
		Method:      req.CodeChallengeMethod,
		Resource:    req.Resource,
	}

	client, resource, err := s.validateAuthorize(r.Context(), params)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())

		return
	}

	// Dev mode (ORAKO_AUTH_MODE=dev) has no real upstream login to reuse: the
	// SPA already renders an explanatory page instead of the consent screen
	// (AuthorizePage.tsx checks isOidc client-side), so this is defense in
	// depth against the endpoint being hit directly.
	if s.unsupportedMessage != "" {
		writeOAuthError(w, http.StatusBadRequest, "unsupported", s.unsupportedMessage)

		return
	}

	bearer, ok := bearerFromHeader(r)
	if !ok {
		writeOAuthError(w, http.StatusUnauthorized, "unauthorized", "missing or malformed Authorization header")

		return
	}

	if req.Decision == "deny" {
		if _, err := s.humans.Authenticate(r.Context(), bearer); err != nil {
			writeOAuthError(w, http.StatusUnauthorized, "unauthorized", "could not verify your session: "+err.Error())

			return
		}

		redirect, buildErr := buildRedirect(params.RedirectURI, map[string]string{
			"error": "access_denied",
			"iss":   s.baseURL,
			"state": params.State,
		})
		if buildErr != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", buildErr.Error())

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"redirect": redirect})

		return
	}

	projectIDs, perr := textToProjectIDs(req.ProjectIDs)
	if perr != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed project_ids: "+perr.Error())

		return
	}

	orgID, parseErr := uuid.Parse(req.OrgID)
	if parseErr != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "org_id must be a valid UUID")

		return
	}

	member, err := s.humans.AuthenticateForOrg(r.Context(), bearer, orgID)
	if err != nil {
		writeOAuthError(w, http.StatusUnauthorized, "unauthorized", "could not verify your membership in the selected organization: "+err.Error())

		return
	}

	redirect, err := s.mintAuthCode(r.Context(), client, params, resource, member, projectIDs)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", err.Error())

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"redirect": redirect})
}

// mintAuthCode persists a freshly minted authorization code bound to client,
// params, resource and member, and returns redirect_uri with ?code=&state=
// appended — the JSON-API equivalent of phase-2's issueAuthCode, which wrote
// an HTTP 302 directly instead of returning the URL to the caller.
func (s *Server) mintAuthCode(ctx context.Context, client Client, params authorizeParams, resource string, member MemberIdentity, projectIDs []uuid.UUID) (string, error) {
	raw, hash, err := newOpaqueSecret("")
	if err != nil {
		return "", fmt.Errorf("could not generate authorization code: %w", err)
	}

	code := AuthCode{
		ID:                  uuid.New(),
		ClientID:            client.ID,
		RedirectURI:         params.RedirectURI,
		CodeChallenge:       params.Challenge,
		CodeChallengeMethod: params.Method,
		Resource:            resource,
		MemberID:            member.MemberID,
		ProjectIDs:          projectIDs,
		ExpiresAt:           s.now().Add(AuthCodeTTL),
	}

	if err := s.store.CreateAuthCode(ctx, code, hash); err != nil {
		return "", fmt.Errorf("could not store authorization code: %w", err)
	}

	return buildRedirect(params.RedirectURI, map[string]string{
		ResponseTypeCode: raw,
		"iss":            s.baseURL,
		"state":          params.State,
	})
}

// buildRedirect merges params into redirectURI's query string (empty values
// omitted) and returns the resulting absolute URL. redirectURI has already
// been checked as an exact match against the client's registered
// redirect_uris by validateAuthorize before either caller (mintAuthCode, the
// deny branch of ServeAuthorizeApprove) ever reaches this function — never
// attacker-supplied, unvalidated input.
func buildRedirect(redirectURI string, params map[string]string) (string, error) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return "", fmt.Errorf("invalid redirect_uri: %w", err)
	}

	q := u.Query()

	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}

	u.RawQuery = q.Encode()

	return u.String(), nil
}

// bearerFromHeader extracts the raw bearer token from a standard
// "Authorization: Bearer <token>" header — the same header shape the
// dashboard's fetch client (web/src/lib/client.ts) already attaches to every
// Connect-RPC call, so /oauth/authorize/approve introduces no new client
// convention.
func bearerFromHeader(r *http.Request) (string, bool) {
	const prefix = "Bearer "

	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}

	token := strings.TrimSpace(strings.TrimPrefix(h, prefix))

	return token, token != ""
}
