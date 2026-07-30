// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestMetadataDocuments proves the AS metadata and PRM documents advertise
// the shape the MCP Authorization spec and RFC 9728/8414 require: S256-only
// PKCE, both grants (locked by the phase-1 spike), and the PRM naming this
// server as the authorization server for {base}/mcp.
func TestMetadataDocuments(t *testing.T) {
	t.Parallel()

	ts, srv, _, _ := newTestServer(t)

	asResp, err := http.Get(ts.URL + "/.well-known/oauth-authorization-server") //nolint:noctx // test
	if err != nil {
		t.Fatalf("GET AS metadata: %v", err)
	}

	defer asResp.Body.Close() //nolint:errcheck // test helper

	var as map[string]any

	if err := json.NewDecoder(asResp.Body).Decode(&as); err != nil {
		t.Fatalf("decoding AS metadata: %v", err)
	}

	if as["issuer"] != srv.baseURL {
		t.Errorf("issuer = %v, want %v", as["issuer"], srv.baseURL)
	}

	if srv.PRMURL() != srv.baseURL+"/.well-known/oauth-protected-resource/mcp" {
		t.Errorf("PRMURL = %v, want path-scoped MCP metadata URL", srv.PRMURL())
	}

	if as["authorization_response_iss_parameter_supported"] != true {
		t.Errorf(
			"authorization_response_iss_parameter_supported = %v, want true",
			as["authorization_response_iss_parameter_supported"],
		)
	}

	methods, _ := as["code_challenge_methods_supported"].([]any)
	if len(methods) != 1 || methods[0] != "S256" {
		t.Errorf("code_challenge_methods_supported = %v, want [S256] only", methods)
	}

	grants, _ := as["grant_types_supported"].([]any)
	if !containsAny(grants, "authorization_code") || !containsAny(grants, "refresh_token") {
		t.Errorf("grant_types_supported = %v, want both authorization_code and refresh_token", grants)
	}

	for _, path := range []string{
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp",
	} {
		prmResp, err := http.Get(ts.URL + path) //nolint:noctx // test
		if err != nil {
			t.Fatalf("GET PRM %s: %v", path, err)
		}

		var prm map[string]any
		if err := json.NewDecoder(prmResp.Body).Decode(&prm); err != nil {
			prmResp.Body.Close() //nolint:errcheck // test helper
			t.Fatalf("decoding PRM %s: %v", path, err)
		}
		prmResp.Body.Close() //nolint:errcheck // test helper

		if prm["resource"] != srv.ResourceURL() {
			t.Errorf("PRM %s resource = %v, want %v", path, prm["resource"], srv.ResourceURL())
		}

		servers, _ := prm["authorization_servers"].([]any)
		if len(servers) != 1 || servers[0] != srv.baseURL {
			t.Errorf("PRM %s authorization_servers = %v, want [%v]", path, servers, srv.baseURL)
		}
	}
}

func containsAny(list []any, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}

	return false
}

// TestRegisterAcceptsRealClaudeCodeBody exercises DCR with the exact request
// body real Claude Code sent in the phase-1 spike, and checks the response
// always reports a public client (token_endpoint_auth_method: none) even
// though the request already asked for that.
func TestRegisterAcceptsRealClaudeCodeBody(t *testing.T) {
	t.Parallel()

	ts, _, _, _ := newTestServer(t)

	body := `{"client_name":"Claude Code (orako-spike)","redirect_uris":["http://localhost:3118/callback"],"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"token_endpoint_auth_method":"none"}`

	resp, err := http.Post(ts.URL+"/register", "application/json", strings.NewReader(body)) //nolint:noctx // test
	if err != nil {
		t.Fatalf("POST /register: %v", err)
	}

	defer resp.Body.Close() //nolint:errcheck // test helper

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	var out map[string]any

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	if out["token_endpoint_auth_method"] != "none" {
		t.Errorf("token_endpoint_auth_method = %v, want none (public client, no secret ever issued)", out["token_endpoint_auth_method"])
	}

	if out["client_id"] == "" || out["client_id"] == nil {
		t.Error("client_id must be issued")
	}
}

func TestRegisterRejectsMissingRedirectURIs(t *testing.T) {
	t.Parallel()

	ts, _, _, _ := newTestServer(t)

	resp, err := http.Post(ts.URL+"/register", "application/json", strings.NewReader(`{"client_name":"x"}`)) //nolint:noctx // test
	if err != nil {
		t.Fatalf("POST /register: %v", err)
	}

	defer resp.Body.Close() //nolint:errcheck // test helper

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestFullFlowDCRAuthorizeTokenPKCE is the phase-2 acceptance criterion:
// register → authorize (stubbed upstream login) → token with PKCE, and the
// issued access token carries the MCP resource audience.
func TestFullFlowDCRAuthorizeTokenPKCE(t *testing.T) {
	t.Parallel()

	ts, srv, bearer, _ := newTestServer(t)

	redirectURI := "http://localhost:3118/callback"
	clientID := registerClient(t, ts, redirectURI)
	verifier, challenge := pkcePair()

	req := newApproveRequest(clientID, redirectURI, challenge, "", "state-123")
	code, state, issuer := authorizeApprove(t, ts, bearer, req)

	if code == "" {
		t.Fatal("no authorization code returned")
	}

	if state != "state-123" {
		t.Errorf("state = %q, want state-123 (must round-trip unchanged)", state)
	}

	if issuer != srv.baseURL {
		t.Errorf("iss = %q, want %q", issuer, srv.baseURL)
	}

	tokenForm := url.Values{}
	tokenForm.Set("grant_type", "authorization_code")
	tokenForm.Set("code", code)
	tokenForm.Set("code_verifier", verifier)
	tokenForm.Set("client_id", clientID)
	tokenForm.Set("redirect_uri", redirectURI)

	out, status := tokenExchange(t, ts, tokenForm)
	if status != http.StatusOK {
		t.Fatalf("token exchange status = %d, body = %v", status, out)
	}

	access, _ := out["access_token"].(string)
	refresh, _ := out["refresh_token"].(string)

	if access == "" || refresh == "" {
		t.Fatalf("expected both access_token and refresh_token, got %v", out)
	}

	if !strings.HasPrefix(access, AccessTokenPrefix) {
		t.Errorf("access_token does not carry the expected prefix: %q", access)
	}

	tok, err := srv.store.GetToken(t.Context(), access, TokenKindAccess)
	if err != nil {
		t.Fatalf("GetToken(access): %v", err)
	}

	if tok.Resource != srv.ResourceURL() {
		t.Errorf("issued token audience = %q, want %q (defaulted resource)", tok.Resource, srv.ResourceURL())
	}
}

// TestAuthorizeWrongPKCEVerifierRejected proves a mismatched code_verifier is
// rejected at /token even though the code itself is valid and unexpired.
func TestAuthorizeWrongPKCEVerifierRejected(t *testing.T) {
	t.Parallel()

	ts, _, bearer, _ := newTestServer(t)

	redirectURI := "http://localhost:3118/callback"
	clientID := registerClient(t, ts, redirectURI)
	_, challenge := pkcePair()

	req := newApproveRequest(clientID, redirectURI, challenge, "", "s")
	code, _, _ := authorizeApprove(t, ts, bearer, req)

	tokenForm := url.Values{}
	tokenForm.Set("grant_type", "authorization_code")
	tokenForm.Set("code", code)
	tokenForm.Set("code_verifier", "totally-wrong-verifier")
	tokenForm.Set("client_id", clientID)
	tokenForm.Set("redirect_uri", redirectURI)

	out, status := tokenExchange(t, ts, tokenForm)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (invalid_grant)", status)
	}

	if out["error"] != "invalid_grant" {
		t.Errorf("error = %v, want invalid_grant", out["error"])
	}
}

// TestAuthorizeExactRedirectURIEnforced proves an unregistered redirect_uri
// is rejected at the approve endpoint without ever returning a redirect.
func TestAuthorizeExactRedirectURIEnforced(t *testing.T) {
	t.Parallel()

	ts, _, bearer, _ := newTestServer(t)

	registeredURI := "http://localhost:3118/callback"
	clientID := registerClient(t, ts, registeredURI)
	_, challenge := pkcePair()

	req := newApproveRequest(clientID, "http://localhost:9999/callback", challenge, "", "s")
	req.Decision = "approve"

	redirect := postApprove(t, ts, bearer, req, http.StatusBadRequest)
	if redirect != "" {
		t.Fatal("an unregistered redirect_uri must never produce a redirect")
	}
}

// TestAuthorizeDenyRedirectsWithAccessDenied proves declining consent
// returns a redirect target carrying error=access_denied rather than issuing
// a code — and that it still requires a valid session, exactly like approve.
func TestAuthorizeDenyRedirectsWithAccessDenied(t *testing.T) {
	t.Parallel()

	ts, srv, bearer, _ := newTestServer(t)

	redirectURI := "http://localhost:3118/callback"
	clientID := registerClient(t, ts, redirectURI)
	_, challenge := pkcePair()

	req := newApproveRequest(clientID, redirectURI, challenge, "", "s")
	req.Decision = "deny"

	redirect := postApprove(t, ts, bearer, req, http.StatusOK)

	loc, err := url.Parse(redirect)
	if err != nil {
		t.Fatalf("parsing redirect: %v", err)
	}

	if loc.Query().Get("error") != "access_denied" {
		t.Errorf("error = %q, want access_denied", loc.Query().Get("error"))
	}

	if loc.Query().Get("state") != "s" {
		t.Errorf("state = %q, want %q (must round-trip unchanged)", loc.Query().Get("state"), "s")
	}

	if loc.Query().Get("iss") != srv.baseURL {
		t.Errorf("iss = %q, want %q", loc.Query().Get("iss"), srv.baseURL)
	}
}

// TestAuthorizeInvalidBearerRejected proves a bad/missing bearer at the
// approve endpoint never issues a code and never returns a redirect, for
// either consent decision.
func TestAuthorizeInvalidBearerRejected(t *testing.T) {
	t.Parallel()

	ts, _, _, _ := newTestServer(t)

	redirectURI := "http://localhost:3118/callback"
	clientID := registerClient(t, ts, redirectURI)
	_, challenge := pkcePair()

	approveReq := newApproveRequest(clientID, redirectURI, challenge, "", "s")
	approveReq.Decision = "approve"

	if redirect := postApprove(t, ts, "not-a-real-session", approveReq, http.StatusUnauthorized); redirect != "" {
		t.Error("an invalid bearer must never return a redirect for an approve decision")
	}

	denyReq := newApproveRequest(clientID, redirectURI, challenge, "", "s")
	denyReq.Decision = "deny"

	if redirect := postApprove(t, ts, "", denyReq, http.StatusUnauthorized); redirect != "" {
		t.Error("a missing bearer must never return a redirect, even for a deny decision")
	}
}

// TestAuthorizeClientInfoPublic proves the pre-consent client-info lookup —
// the JSON API's replacement for the old server-rendered consent page's
// client-name display — needs no Authorization header and reuses
// validateAuthorize, so a bad request is rejected the same way the approve
// endpoint rejects it.
func TestAuthorizeClientInfoPublic(t *testing.T) {
	t.Parallel()

	ts, _, _, _ := newTestServer(t)

	redirectURI := "http://localhost:3118/callback"
	clientID := registerClient(t, ts, redirectURI)
	_, challenge := pkcePair()

	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", PKCEMethodS256)

	resp, err := http.Get(ts.URL + "/oauth/authorize/client?" + q.Encode()) //nolint:noctx // test
	if err != nil {
		t.Fatalf("GET /oauth/authorize/client: %v", err)
	}

	defer resp.Body.Close() //nolint:errcheck // test helper

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var out map[string]any

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	if out["client_name"] != "Claude Code (test)" {
		t.Errorf("client_name = %v, want %q", out["client_name"], "Claude Code (test)")
	}

	badResp, err := http.Get(ts.URL + "/oauth/authorize/client?client_id=unknown-client&redirect_uri=" + url.QueryEscape(redirectURI)) //nolint:noctx // test
	if err != nil {
		t.Fatalf("GET /oauth/authorize/client (unknown client): %v", err)
	}

	defer badResp.Body.Close() //nolint:errcheck // test helper

	if badResp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown client_id status = %d, want 400", badResp.StatusCode)
	}
}

// TestRefreshGrantRotatesAndDetectsReuse exercises the refresh_token grant:
// rotation issues a fresh pair, and replaying the now-rotated-away refresh
// token is treated as theft — the entire grant (including the still-valid
// access token from the rotation) is revoked.
func TestRefreshGrantRotatesAndDetectsReuse(t *testing.T) {
	t.Parallel()

	ts, srv, bearer, _ := newTestServer(t)

	redirectURI := "http://localhost:3118/callback"
	clientID := registerClient(t, ts, redirectURI)
	verifier, challenge := pkcePair()

	req := newApproveRequest(clientID, redirectURI, challenge, "", "s")
	code, _, _ := authorizeApprove(t, ts, bearer, req)

	tokenForm := url.Values{}
	tokenForm.Set("grant_type", "authorization_code")
	tokenForm.Set("code", code)
	tokenForm.Set("code_verifier", verifier)
	tokenForm.Set("client_id", clientID)
	tokenForm.Set("redirect_uri", redirectURI)

	first, status := tokenExchange(t, ts, tokenForm)
	if status != http.StatusOK {
		t.Fatalf("initial token exchange failed: %d %v", status, first)
	}

	firstAccess, _ := first["access_token"].(string)
	firstRefresh, _ := first["refresh_token"].(string)

	refreshForm := url.Values{}
	refreshForm.Set("grant_type", "refresh_token")
	refreshForm.Set("refresh_token", firstRefresh)
	refreshForm.Set("client_id", clientID)

	second, status := tokenExchange(t, ts, refreshForm)
	if status != http.StatusOK {
		t.Fatalf("refresh grant failed: %d %v", status, second)
	}

	secondAccess, _ := second["access_token"].(string)
	secondRefresh, _ := second["refresh_token"].(string)

	if secondAccess == firstAccess || secondRefresh == firstRefresh {
		t.Fatal("rotation must issue brand-new access and refresh secrets")
	}

	// Replaying the now-rotated-away first refresh token is reuse.
	reuseForm := url.Values{}
	reuseForm.Set("grant_type", "refresh_token")
	reuseForm.Set("refresh_token", firstRefresh)
	reuseForm.Set("client_id", clientID)

	reuseOut, reuseStatus := tokenExchange(t, ts, reuseForm)
	if reuseStatus != http.StatusBadRequest {
		t.Fatalf("reuse status = %d, want 400", reuseStatus)
	}

	if reuseOut["error"] != "invalid_grant" {
		t.Errorf("reuse error = %v, want invalid_grant", reuseOut["error"])
	}

	// The theft response must revoke the WHOLE grant, including the access
	// token minted by the (legitimate) rotation that just happened.
	tok, err := srv.store.GetToken(t.Context(), secondAccess, TokenKindAccess)
	if err != nil {
		t.Fatalf("GetToken(secondAccess): %v", err)
	}

	if !tok.Revoked() {
		t.Error("refresh token reuse must revoke every token sharing the grant, including the latest access token")
	}
}

func TestRefreshGrantConcurrentRotationOnlyOneSucceeds(t *testing.T) {
	t.Parallel()

	ts, srv, bearer, _ := newTestServer(t)

	redirectURI := "http://localhost:3118/callback"
	clientID := registerClient(t, ts, redirectURI)
	verifier, challenge := pkcePair()

	req := newApproveRequest(clientID, redirectURI, challenge, "", "s")
	code, _, _ := authorizeApprove(t, ts, bearer, req)

	tokenForm := url.Values{}
	tokenForm.Set("grant_type", "authorization_code")
	tokenForm.Set("code", code)
	tokenForm.Set("code_verifier", verifier)
	tokenForm.Set("client_id", clientID)
	tokenForm.Set("redirect_uri", redirectURI)

	first, status := tokenExchange(t, ts, tokenForm)
	if status != http.StatusOK {
		t.Fatalf("initial token exchange failed: %d %v", status, first)
	}

	firstRefresh, _ := first["refresh_token"].(string)

	const concurrentRotations = 8

	start := make(chan struct{})
	results := make(chan refreshRotationResult, concurrentRotations)

	for range concurrentRotations {
		go func() {
			<-start
			results <- postRefreshGrant(ts.URL, firstRefresh, clientID)
		}()
	}

	close(start)

	var (
		successes    int
		invalidGrant int
		accessToken  string
	)

	for range concurrentRotations {
		result := <-results
		switch {
		case result.err != nil:
			t.Fatalf("concurrent refresh request: %v", result.err)
		case result.status == http.StatusOK:
			successes++
			accessToken, _ = result.body["access_token"].(string)
		case result.status == http.StatusBadRequest && result.body["error"] == "invalid_grant":
			invalidGrant++
		default:
			t.Fatalf("unexpected concurrent refresh response: status=%d body=%v", result.status, result.body)
		}
	}

	if successes != 1 {
		t.Fatalf("successful rotations = %d, want exactly 1", successes)
	}

	if invalidGrant != concurrentRotations-1 {
		t.Fatalf("invalid_grant responses = %d, want %d", invalidGrant, concurrentRotations-1)
	}

	rotatedAccess, err := srv.store.GetToken(t.Context(), accessToken, TokenKindAccess)
	if err != nil {
		t.Fatalf("GetToken(rotated access): %v", err)
	}

	if !rotatedAccess.Revoked() {
		t.Error("concurrent refresh reuse must revoke the grant produced by the winning rotation")
	}
}

type refreshRotationResult struct {
	body   map[string]any
	status int
	err    error
}

func postRefreshGrant(baseURL, refreshToken, clientID string) refreshRotationResult {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)

	resp, err := http.PostForm(baseURL+"/token", form) //nolint:noctx // concurrent integration-test helper
	if err != nil {
		return refreshRotationResult{err: err}
	}
	defer resp.Body.Close() //nolint:errcheck // integration-test helper

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return refreshRotationResult{status: resp.StatusCode, err: err}
	}

	return refreshRotationResult{body: body, status: resp.StatusCode}
}

// TestTokenResourceMismatchRejected proves RFC 8707: a resource repeated at
// /token that differs from the one bound to the code at /authorize is
// rejected rather than silently accepted.
func TestTokenResourceMismatchRejected(t *testing.T) {
	t.Parallel()

	ts, _, bearer, _ := newTestServer(t)

	redirectURI := "http://localhost:3118/callback"
	clientID := registerClient(t, ts, redirectURI)
	verifier, challenge := pkcePair()

	req := newApproveRequest(clientID, redirectURI, challenge, "https://orako.example.com/mcp", "s")
	code, _, _ := authorizeApprove(t, ts, bearer, req)

	tokenForm := url.Values{}
	tokenForm.Set("grant_type", "authorization_code")
	tokenForm.Set("code", code)
	tokenForm.Set("code_verifier", verifier)
	tokenForm.Set("client_id", clientID)
	tokenForm.Set("redirect_uri", redirectURI)
	tokenForm.Set("resource", "https://attacker.example.com/mcp")

	out, status := tokenExchange(t, ts, tokenForm)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}

	if out["error"] != "invalid_target" {
		t.Errorf("error = %v, want invalid_target", out["error"])
	}
}

// TestAuthorizeDevModeUnsupportedDoesNotCrash proves the dev-mode posture:
// the approve endpoint rejects with the configured message instead of
// panicking (s.humans is nil in dev mode; the unsupportedMessage check must
// short-circuit before ever calling it) and issues no code/redirect. The SPA
// itself never even reaches this endpoint in dev mode (AuthorizePage.tsx
// checks isOidc first) — this is the server-side defense-in-depth path.
func TestAuthorizeDevModeUnsupportedDoesNotCrash(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := NewStore(pool)

	srv := NewServer("", store, nil, "This server runs in dev mode; remote MCP OAuth is not supported.")

	mux := chi.NewRouter()
	srv.RegisterRoutes(mux)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	srv.baseURL = ts.URL

	redirectURI := "http://localhost:3118/callback"
	clientID := registerClient(t, ts, redirectURI)
	_, challenge := pkcePair()

	req := newApproveRequest(clientID, redirectURI, challenge, "", "s")
	req.Decision = "approve"

	if redirect := postApprove(t, ts, "irrelevant-bearer", req, http.StatusBadRequest); redirect != "" {
		t.Error("dev-mode unsupported must never return a redirect")
	}
}
