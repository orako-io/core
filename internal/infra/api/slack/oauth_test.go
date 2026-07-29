// SPDX-License-Identifier: AGPL-3.0-or-later

package slack_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	slackhttp "github.com/orako-io/core/internal/infra/api/slack"
)

// ── fakes ─────────────────────────────────────────────────────────────────────

// fakeProviderConfigurator records Configure calls for assertions.
type fakeProviderConfigurator struct {
	calls []configureCall
	err   error
}

type configureCall struct {
	projectID uuid.UUID
	kind      string
	creds     map[string]string
}

func (f *fakeProviderConfigurator) Configure(_ context.Context, projectID uuid.UUID, kind string, creds map[string]string) error {
	if f.err != nil {
		return f.err
	}

	f.calls = append(f.calls, configureCall{projectID, kind, creds})

	return nil
}

type fakeInstallAuthenticator struct {
	principal slackhttp.InstallPrincipal
	err       error
}

func (f fakeInstallAuthenticator) Authenticate(_ context.Context, authorizationHeader string) (slackhttp.InstallPrincipal, error) {
	if f.err != nil {
		return slackhttp.InstallPrincipal{}, f.err
	}

	if authorizationHeader != "Bearer valid-session" {
		return slackhttp.InstallPrincipal{}, errors.New("invalid session")
	}

	return f.principal, nil
}

type fakeInstallAuthorizer struct {
	orgID           uuid.UUID
	allowedProject  uuid.UUID
	returnedProject uuid.UUID
	err             error
}

func (f fakeInstallAuthorizer) Authorize(
	_ context.Context,
	principal slackhttp.InstallPrincipal,
	requestedProjectID uuid.UUID,
) (slackhttp.InstallAuthorization, error) {
	if f.err != nil {
		return slackhttp.InstallAuthorization{}, f.err
	}

	if !principal.IsOrgAdmin {
		return slackhttp.InstallAuthorization{}, errors.New("org admin required")
	}

	if principal.OrgID != f.orgID {
		return slackhttp.InstallAuthorization{}, errors.New("principal organization mismatch")
	}

	if f.allowedProject != uuid.Nil && requestedProjectID != f.allowedProject {
		return slackhttp.InstallAuthorization{}, errors.New("project belongs to another organization")
	}

	authorizedProject := requestedProjectID
	if f.returnedProject != uuid.Nil {
		authorizedProject = f.returnedProject
	}

	return slackhttp.InstallAuthorization{
		MemberID:  principal.MemberID,
		OrgID:     f.orgID,
		ProjectID: authorizedProject,
	}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// fakeSlackServer returns a test Slack API server that handles oauth.v2.access.
// The given botToken is returned in the access_token field of a successful response.
func fakeSlackServer(t *testing.T, botToken, teamID, teamName, appID string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oauth.v2.access" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":           true,
			"access_token": botToken,
			"app_id":       appID,
			"team": map[string]string{
				"id":   teamID,
				"name": teamName,
			},
		})
	}))
}

// buildOAuthHandler builds an OAuthHandler pointed at the given fake Slack server.
func buildOAuthHandler(
	slackSrv *httptest.Server,
	configurator *fakeProviderConfigurator,
	baseURL string,
) *slackhttp.OAuthHandler {
	orgID := uuid.New()
	cfg := slackhttp.OAuthConfig{
		ClientID:      "test_client_id",
		ClientSecret:  "test_client_secret",
		SigningSecret: "test_signing_secret",
		BaseURL:       baseURL,
		SlackBaseURL:  slackSrv.URL,
		HTTPClient:    slackSrv.Client(),
		InstallAuthenticator: fakeInstallAuthenticator{principal: slackhttp.InstallPrincipal{
			MemberID:   uuid.New(),
			OrgID:      orgID,
			IsOrgAdmin: true,
		}},
		InstallAuthorizer: fakeInstallAuthorizer{orgID: orgID},
	}

	return slackhttp.NewOAuthHandler(cfg, configurator)
}

func authorizeURLFromInstall(t *testing.T, recorder *httptest.ResponseRecorder) *url.URL {
	t.Helper()

	if recorder.Code != http.StatusOK {
		t.Fatalf("install status = %d, want 200; body: %s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		AuthorizeURL string `json:"authorize_url"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decoding install response: %v", err)
	}

	parsed, err := url.Parse(response.AuthorizeURL)
	if err != nil {
		t.Fatalf("parsing authorize URL: %v", err)
	}

	return parsed
}

func authenticatedInstallRequest(projectID uuid.UUID) *http.Request {
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/slack/oauth/install?project_id="+projectID.String(),
		nil,
	)
	req.Header.Set("Authorization", "Bearer valid-session")

	return req
}

// ── tests ──────────────────────────────────────────────────────────────────────

func TestOAuth_InstallReturnsAuthorizedSlackURL(t *testing.T) {
	t.Parallel()

	slackSrv := fakeSlackServer(t, "xoxb-token", "T001", "TestTeam", "A001")
	defer slackSrv.Close()

	configurator := &fakeProviderConfigurator{}

	// Mount a Orako server just to capture the install request.
	orakoSrv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer orakoSrv.Close()

	handler := buildOAuthHandler(slackSrv, configurator, orakoSrv.URL)

	projectID := uuid.New()
	req := authenticatedInstallRequest(projectID)
	w := httptest.NewRecorder()
	handler.ServeInstall(w, req)

	location := authorizeURLFromInstall(t, w)

	// The redirect URL must point to Slack's authorization endpoint.
	if !strings.Contains(location.String(), slackSrv.URL+"/oauth/v2/authorize") {
		t.Errorf("Location %q does not contain expected auth URL", location)
	}

	// The redirect URL must include a state parameter.
	if location.Query().Get("state") == "" {
		t.Error("state parameter is missing from redirect URL")
	}
}

func TestOAuth_InstallRequiresBearer(t *testing.T) {
	t.Parallel()

	slackSrv := fakeSlackServer(t, "xoxb-token", "T001", "TestTeam", "A001")
	defer slackSrv.Close()

	handler := buildOAuthHandler(slackSrv, &fakeProviderConfigurator{}, "https://orako.example.com")
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/slack/oauth/install?project_id="+uuid.NewString(),
		nil,
	)
	w := httptest.NewRecorder()

	handler.ServeInstall(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestOAuth_InstallRejectsCrossOrgProject(t *testing.T) {
	t.Parallel()

	slackSrv := fakeSlackServer(t, "xoxb-token", "T001", "TestTeam", "A001")
	defer slackSrv.Close()

	allowedProject := uuid.New()
	foreignProject := uuid.New()
	orgID := uuid.New()
	cfg := slackhttp.OAuthConfig{
		ClientID:      "test_client_id",
		ClientSecret:  "test_client_secret",
		SigningSecret: "test_signing_secret",
		BaseURL:       "https://orako.example.com",
		SlackBaseURL:  slackSrv.URL,
		HTTPClient:    slackSrv.Client(),
		InstallAuthenticator: fakeInstallAuthenticator{principal: slackhttp.InstallPrincipal{
			MemberID:   uuid.New(),
			OrgID:      orgID,
			IsOrgAdmin: true,
		}},
		InstallAuthorizer: fakeInstallAuthorizer{
			orgID:          orgID,
			allowedProject: allowedProject,
		},
	}
	handler := slackhttp.NewOAuthHandler(cfg, &fakeProviderConfigurator{})
	w := httptest.NewRecorder()

	handler.ServeInstall(w, authenticatedInstallRequest(foreignProject))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for cross-org project", w.Code)
	}
}

func TestOAuth_InstallRejectsNonAdmin(t *testing.T) {
	t.Parallel()

	slackSrv := fakeSlackServer(t, "xoxb-token", "T001", "TestTeam", "A001")
	defer slackSrv.Close()

	orgID := uuid.New()
	cfg := slackhttp.OAuthConfig{
		ClientID:      "test_client_id",
		ClientSecret:  "test_client_secret",
		SigningSecret: "test_signing_secret",
		BaseURL:       "https://orako.example.com",
		SlackBaseURL:  slackSrv.URL,
		HTTPClient:    slackSrv.Client(),
		InstallAuthenticator: fakeInstallAuthenticator{principal: slackhttp.InstallPrincipal{
			MemberID:   uuid.New(),
			OrgID:      orgID,
			IsOrgAdmin: false,
		}},
		InstallAuthorizer: fakeInstallAuthorizer{orgID: orgID},
	}
	handler := slackhttp.NewOAuthHandler(cfg, &fakeProviderConfigurator{})
	w := httptest.NewRecorder()

	handler.ServeInstall(w, authenticatedInstallRequest(uuid.New()))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for non-admin caller", w.Code)
	}
}

func TestOAuth_InstallRejectsMismatchedAuthorization(t *testing.T) {
	t.Parallel()

	slackSrv := fakeSlackServer(t, "xoxb-token", "T001", "TestTeam", "A001")
	defer slackSrv.Close()

	requestedProject := uuid.New()
	orgID := uuid.New()
	cfg := slackhttp.OAuthConfig{
		ClientID:      "test_client_id",
		ClientSecret:  "test_client_secret",
		SigningSecret: "test_signing_secret",
		BaseURL:       "https://orako.example.com",
		SlackBaseURL:  slackSrv.URL,
		HTTPClient:    slackSrv.Client(),
		InstallAuthenticator: fakeInstallAuthenticator{principal: slackhttp.InstallPrincipal{
			MemberID:   uuid.New(),
			OrgID:      orgID,
			IsOrgAdmin: true,
		}},
		InstallAuthorizer: fakeInstallAuthorizer{
			orgID:           orgID,
			returnedProject: uuid.New(),
		},
	}
	handler := slackhttp.NewOAuthHandler(cfg, &fakeProviderConfigurator{})
	w := httptest.NewRecorder()

	handler.ServeInstall(w, authenticatedInstallRequest(requestedProject))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for mismatched authorization", w.Code)
	}
}

func TestOAuth_InstallRejectsMissingProjectID(t *testing.T) {
	t.Parallel()

	slackSrv := fakeSlackServer(t, "xoxb-token", "T001", "TestTeam", "A001")
	defer slackSrv.Close()

	configurator := &fakeProviderConfigurator{}

	orakoSrv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer orakoSrv.Close()

	handler := buildOAuthHandler(slackSrv, configurator, orakoSrv.URL)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/slack/oauth/install", nil)
	req.Header.Set("Authorization", "Bearer valid-session")
	w := httptest.NewRecorder()
	handler.ServeInstall(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for missing project_id", w.Code)
	}
}

func TestOAuth_CallbackConfiguresProviderThroughCommand(t *testing.T) {
	t.Parallel()

	const (
		botToken = "xoxb-abc-def-ghi"
		teamID   = "T12345"
		teamName = "Orako Corp"
		appID    = "A98765"
	)

	slackSrv := fakeSlackServer(t, botToken, teamID, teamName, appID)
	defer slackSrv.Close()

	configurator := &fakeProviderConfigurator{}

	orakoSrv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer orakoSrv.Close()

	handler := buildOAuthHandler(slackSrv, configurator, orakoSrv.URL)

	projectID := uuid.New()

	// Step 1: obtain a valid state token via the install endpoint.
	installReq := authenticatedInstallRequest(projectID)
	installW := httptest.NewRecorder()
	handler.ServeInstall(installW, installReq)

	location := authorizeURLFromInstall(t, installW)

	state := location.Query().Get("state")
	if state == "" {
		t.Fatal("no state in install redirect")
	}

	// Step 2: simulate the OAuth callback.
	callbackURL := "/slack/oauth/callback?code=fake_code&state=" + url.QueryEscape(state)
	callbackReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, callbackURL, nil)
	callbackW := httptest.NewRecorder()
	handler.ServeCallback(callbackW, callbackReq)

	if callbackW.Code != http.StatusOK {
		t.Fatalf("callback status = %d, want 200; body: %s", callbackW.Code, callbackW.Body.String())
	}

	// Assert configurator was called through the command (no direct store/registry bypass).
	if len(configurator.calls) != 1 {
		t.Fatalf("configurator.Configure called %d times, want 1", len(configurator.calls))
	}

	got := configurator.calls[0]

	if got.projectID != projectID {
		t.Errorf("projectID: got %v, want %v", got.projectID, projectID)
	}

	if got.kind != "slack" {
		t.Errorf("kind: got %q, want %q", got.kind, "slack")
	}

	if got.creds["bot_token"] != botToken {
		t.Errorf("credentials[bot_token]: got %q, want %q", got.creds["bot_token"], botToken)
	}

	// signing_secret comes from OAuthConfig.SigningSecret.
	if got.creds["signing_secret"] != "test_signing_secret" {
		t.Errorf("credentials[signing_secret]: got %q, want %q", got.creds["signing_secret"], "test_signing_secret")
	}

	// team metadata is forwarded too.
	if got.creds["team_id"] != teamID {
		t.Errorf("credentials[team_id]: got %q, want %q", got.creds["team_id"], teamID)
	}
}

func TestOAuth_CallbackRejectsMissingState(t *testing.T) {
	t.Parallel()

	slackSrv := fakeSlackServer(t, "xoxb-token", "T001", "TestTeam", "A001")
	defer slackSrv.Close()

	configurator := &fakeProviderConfigurator{}
	orakoSrv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer orakoSrv.Close()

	handler := buildOAuthHandler(slackSrv, configurator, orakoSrv.URL)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/slack/oauth/callback?code=abc", nil)
	w := httptest.NewRecorder()
	handler.ServeCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for missing state", w.Code)
	}
}

func TestOAuth_CallbackRejectsInvalidState(t *testing.T) {
	t.Parallel()

	slackSrv := fakeSlackServer(t, "xoxb-token", "T001", "TestTeam", "A001")
	defer slackSrv.Close()

	configurator := &fakeProviderConfigurator{}
	orakoSrv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer orakoSrv.Close()

	handler := buildOAuthHandler(slackSrv, configurator, orakoSrv.URL)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/slack/oauth/callback?code=abc&state=notavalidstate", nil)
	w := httptest.NewRecorder()
	handler.ServeCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for invalid state", w.Code)
	}
}

func TestOAuth_CallbackStateOneTimeUse(t *testing.T) {
	t.Parallel()

	slackSrv := fakeSlackServer(t, "xoxb-token", "T001", "TestTeam", "A001")
	defer slackSrv.Close()

	configurator := &fakeProviderConfigurator{}
	orakoSrv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer orakoSrv.Close()

	handler := buildOAuthHandler(slackSrv, configurator, orakoSrv.URL)
	projectID := uuid.New()

	// Obtain state.
	installReq := authenticatedInstallRequest(projectID)
	installW := httptest.NewRecorder()
	handler.ServeInstall(installW, installReq)

	location := authorizeURLFromInstall(t, installW)
	state := location.Query().Get("state")

	callbackURL := "/slack/oauth/callback?code=fake_code&state=" + url.QueryEscape(state)

	// First use succeeds.
	w1 := httptest.NewRecorder()
	handler.ServeCallback(w1, httptest.NewRequestWithContext(context.Background(), http.MethodGet, callbackURL, nil))

	if w1.Code != http.StatusOK {
		t.Fatalf("first callback: status = %d, want 200", w1.Code)
	}

	// Second use with the same state must fail.
	w2 := httptest.NewRecorder()
	handler.ServeCallback(w2, httptest.NewRequestWithContext(context.Background(), http.MethodGet, callbackURL, nil))

	if w2.Code != http.StatusBadRequest {
		t.Fatalf("second callback: status = %d, want 400 (state reuse)", w2.Code)
	}
}
