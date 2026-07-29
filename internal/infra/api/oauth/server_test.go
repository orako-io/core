// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/testsupport"
)

// fakeHumanAuthenticator is the "stubbed upstream login" the acceptance
// criteria call for: it authenticates a fixed bearer as a fixed member,
// standing in for the real ORAKO_AUTH_MODE-backed adapter (transport.
// HumanAuthenticatorAdapter), which is covered by its own tests.
type fakeHumanAuthenticator struct {
	validBearer string
	member      MemberIdentity
}

func (f fakeHumanAuthenticator) Authenticate(_ context.Context, bearer string) (MemberIdentity, error) {
	if bearer == "" || bearer != f.validBearer {
		return MemberIdentity{}, errors.New("invalid session")
	}

	return f.member, nil
}

func (f fakeHumanAuthenticator) AuthenticateForOrg(_ context.Context, bearer string, _ uuid.UUID) (MemberIdentity, error) {
	if bearer == "" || bearer != f.validBearer {
		return MemberIdentity{}, errors.New("invalid session")
	}

	return f.member, nil
}

func (f fakeHumanAuthenticator) AuthenticateAccount(_ context.Context, bearer string) (AccountIdentity, error) {
	if bearer == "" || bearer != f.validBearer {
		return AccountIdentity{}, errors.New("invalid session")
	}

	return AccountIdentity{MemberID: f.member.MemberID, Email: f.member.Email}, nil
}

// newTestServer wires a Server backed by real Postgres (RequirePostgres) on
// an httptest.Server, plus a seeded member and the stubbed human
// authenticator that approves as that member.
func newTestServer(t *testing.T) (*httptest.Server, *Server, string, uuid.UUID) {
	t.Helper()

	pool := testsupport.RequirePostgres(t)
	store := NewStore(pool)
	memberID := seedMember(t, pool)

	bearer := "session-" + uuid.NewString()
	humans := fakeHumanAuthenticator{validBearer: bearer, member: MemberIdentity{MemberID: memberID, Email: "responder@example.com"}}

	srv := NewServer("", store, humans, "")

	mux := chi.NewRouter()
	srv.RegisterRoutes(mux)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	srv.baseURL = ts.URL

	return ts, srv, bearer, memberID
}

// pkcePair returns a random verifier and its S256 challenge.
func pkcePair() (verifier, challenge string) {
	verifier = uuid.NewString() + uuid.NewString()
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])

	return verifier, challenge
}

// registerClient drives POST /register exactly like real Claude Code's DCR
// body (see spike-findings.md §3) and returns the issued client_id.
func registerClient(t *testing.T, ts *httptest.Server, redirectURI string) string {
	t.Helper()

	body := `{"client_name":"Claude Code (test)","redirect_uris":["` + redirectURI + `"],"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"token_endpoint_auth_method":"none"}`

	resp, err := http.Post(ts.URL+"/register", "application/json", strings.NewReader(body)) //nolint:noctx // test helper, ctx not load-bearing
	if err != nil {
		t.Fatalf("POST /register: %v", err)
	}

	defer resp.Body.Close() //nolint:errcheck // test helper

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /register: status %d", resp.StatusCode)
	}

	var out struct {
		ClientID string `json:"client_id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding register response: %v", err)
	}

	return out.ClientID
}

// authorizeApprove drives the authenticated JSON approve endpoint with
// decision=approve and returns the code, state, and issuer extracted from the
// "redirect" field of a 200 response.
func authorizeApprove(t *testing.T, ts *httptest.Server, bearer string, req approveRequest) (code, state, issuer string) {
	t.Helper()

	req.Decision = "approve"

	redirect := postApprove(t, ts, bearer, req, http.StatusOK)

	loc, err := url.Parse(redirect)
	if err != nil {
		t.Fatalf("parsing redirect: %v", err)
	}

	return loc.Query().Get(ResponseTypeCode), loc.Query().Get("state"), loc.Query().Get("iss")
}

// postApprove POSTs req as JSON to /oauth/authorize/approve, attaching
// bearer as a standard "Authorization: Bearer …" header when non-empty
// (mirroring what the SPA's fetch client sends), and returns the decoded
// "redirect" field. Fails the test if the response status doesn't match
// wantStatus.
func postApprove(t *testing.T, ts *httptest.Server, bearer string, req approveRequest, wantStatus int) string {
	t.Helper()

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling approve request: %v", err)
	}

	httpReq, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/oauth/authorize/approve", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	if bearer != "" {
		httpReq.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("POST /oauth/authorize/approve: %v", err)
	}

	defer resp.Body.Close() //nolint:errcheck // test helper

	var out map[string]any

	if decErr := json.NewDecoder(resp.Body).Decode(&out); decErr != nil {
		t.Fatalf("decoding approve response: %v", decErr)
	}

	if resp.StatusCode != wantStatus {
		t.Fatalf("POST /oauth/authorize/approve: status = %d, want %d, body = %v", resp.StatusCode, wantStatus, out)
	}

	redirect, _ := out["redirect"].(string)

	return redirect
}

// newApproveRequest builds an approveRequest for the given OAuth params;
// Decision is left empty (callers set it via authorizeApprove or directly).
func newApproveRequest(clientID, redirectURI, challenge, resource, state string) approveRequest {
	return approveRequest{
		ClientID:            clientID,
		RedirectURI:         redirectURI,
		State:               state,
		CodeChallenge:       challenge,
		CodeChallengeMethod: PKCEMethodS256,
		Resource:            resource,
		OrgID:               uuid.NewString(),
	}
}

// tokenExchange POSTs the authorization_code grant and returns the decoded
// response body (empty map + status on a non-200).
func tokenExchange(t *testing.T, ts *httptest.Server, form url.Values) (map[string]any, int) {
	t.Helper()

	resp, err := http.PostForm(ts.URL+"/token", form) //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("POST /token: %v", err)
	}

	defer resp.Body.Close() //nolint:errcheck // test helper

	var out map[string]any

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding token response: %v", err)
	}

	return out, resp.StatusCode
}
