// SPDX-License-Identifier: AGPL-3.0-or-later

package teams_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	josejwk "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/provider"
	teamsadapter "github.com/orako-io/core/internal/adapters/provider/teams"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/service"
	teamstransport "github.com/orako-io/core/internal/infra/api/teams"
)

const testKeyID = "test-kid"

// testIdentityProvider fakes the Bot Framework's OpenID metadata + JWKS
// endpoints and signs test JWTs with the same key it publishes.
type testIdentityProvider struct {
	server *httptest.Server
	priv   *rsa.PrivateKey
}

func newTestIdentityProvider(t *testing.T) *testIdentityProvider {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}

	idp := &testIdentityProvider{priv: priv}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/.well-known/openidconfiguration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":   "https://api.botframework.com",
			"jwks_uri": idp.server.URL + "/v1/.well-known/keys",
		})
	})
	mux.HandleFunc("/v1/.well-known/keys", func(w http.ResponseWriter, _ *http.Request) {
		jwk := josejwk.JSONWebKey{Key: &priv.PublicKey, KeyID: testKeyID, Algorithm: "RS256", Use: "sig"}

		set := josejwk.JSONWebKeySet{Keys: []josejwk.JSONWebKey{jwk}}
		_ = json.NewEncoder(w).Encode(set)
	})

	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)

	return idp
}

// token signs a JWT with the given issuer/audience/expiry using the idp's
// private key and testKeyID — a token any RemoteKeySet fetching from this
// idp's /keys endpoint will successfully verify (when iss/aud/exp are valid).
func (idp *testIdentityProvider) token(t *testing.T, issuer, audience string, expiresIn time.Duration) string {
	t.Helper()

	claims := jwt.RegisteredClaims{
		Issuer:    issuer,
		Audience:  jwt.ClaimStrings{audience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiresIn)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = testKeyID

	signed, err := tok.SignedString(idp.priv)
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}

	return signed
}

// tokenSignedByOtherKey signs a structurally-valid token with a DIFFERENT key
// than the one published at /keys — simulates a forged signature.
func (idp *testIdentityProvider) tokenSignedByOtherKey(t *testing.T, issuer, audience string) string {
	t.Helper()

	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}

	claims := jwt.RegisteredClaims{
		Issuer:    issuer,
		Audience:  jwt.ClaimStrings{audience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = testKeyID

	signed, err := tok.SignedString(other)
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}

	return signed
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// testHarness wires a WebhookHandler behind a chi router, backed by fakes the
// test configures directly.
type testHarness struct {
	router   chi.Router
	registry *fakeRegistry
	configs  *fakeConfigStore
	followUp *fakeFollowUpper
	provider *fakeTeamsProvider
}

func newTestHarness(t *testing.T, idp *testIdentityProvider, projectID uuid.UUID, botAppID string) *testHarness {
	t.Helper()

	registry := newFakeRegistry()
	configs := newFakeConfigStore()
	followUp := &fakeFollowUpper{err: nil, last: command.FollowUpCommand{}, calls: 0}

	teamsProv := &fakeTeamsProvider{}

	registry.set(projectID, provider.ProviderKindTeams, teamsProv)
	configs.add(projectID, "teams", map[string]string{"bot_app_id": botAppID})

	handler, err := teamstransport.NewWebhookHandler(
		t.Context(),
		idp.server.URL+"/v1/.well-known/openidconfiguration",
		registry, configs, followUp, discardLogger(),
	)
	if err != nil {
		t.Fatalf("NewWebhookHandler: %v", err)
	}

	r := chi.NewRouter()
	handler.RegisterRoutes(r)

	return &testHarness{router: r, registry: registry, configs: configs, followUp: followUp, provider: teamsProv}
}

func doPost(h *testHarness, path, bearer string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, bytes.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)

	return rec
}

// TestServeActivities_LegacyClaimClickAcksWithoutDispatch proves a click on a
// legacy Claim card (delivered before the hub-and-spoke phase 2 teardown) is
// dropped by the real Teams provider's ParseInbound
// (service.ErrUnrecognizedMessage) and acked 200 by the webhook — never
// appended as a reply, never surfaced as an error Bot Framework would retry.
func TestServeActivities_LegacyClaimClickAcksWithoutDispatch(t *testing.T) {
	t.Parallel()

	idp := newTestIdentityProvider(t)
	projectID := uuid.New()
	botAppID := "bot-app-1"

	h := newTestHarness(t, idp, projectID, botAppID)

	// Swap in the REAL Teams adapter so the legacy-click drop is exercised end
	// to end, not simulated by a fake. The nil member/ledger deps are never
	// reached: the claim-action drop happens before any correlation.
	h.registry.set(projectID, provider.ProviderKindTeams, teamsadapter.New(teamsadapter.Config{}, nil, nil))

	tok := idp.token(t, "https://api.botframework.com", botAppID, time.Hour)

	body := []byte(`{"type":"message","text":"Claim","value":{"action":"claim"},"from":{"aadObjectId":"aad-1"},"conversation":{"id":"conv-1"}}`)
	rec := doPost(h, "/teams/activities/"+projectID.String(), tok, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	if h.followUp.calls != 0 {
		t.Errorf("FollowUp must not be dispatched for a legacy Claim click, calls=%d", h.followUp.calls)
	}
}

// TestServeActivities_ValidMessageToken proves a valid JWT with no claim
// action dispatches FollowUp.
func TestServeActivities_ValidMessageToken(t *testing.T) {
	t.Parallel()

	idp := newTestIdentityProvider(t)
	projectID := uuid.New()
	botAppID := "bot-app-1"

	h := newTestHarness(t, idp, projectID, botAppID)

	convID := uuid.New()
	authorID := uuid.New()
	h.provider.inbound = service.InboundMessage{ConversationID: convID, AuthorMemberID: authorID, Body: "here's my answer"}

	tok := idp.token(t, "https://api.botframework.com", botAppID, time.Hour)

	body := []byte(`{"type":"message","text":"here's my answer","conversation":{"id":"conv-1"}}`)
	rec := doPost(h, "/teams/activities/"+projectID.String(), tok, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	if h.followUp.last.ConversationID != convID || h.followUp.last.AuthorMemberID != authorID {
		t.Errorf("FollowUp dispatched with %+v, want {%v %v}", h.followUp.last, convID, authorID)
	}
}

// TestServeActivities_FollowUpFailureReturns500 proves a real dispatch
// failure surfaces as 500 so Bot Framework retries the delivery — a reply is
// a message (hub-and-spoke phase 2), there is no claim rejection or
// second-opinion arm to translate into a special ack anymore.
func TestServeActivities_FollowUpFailureReturns500(t *testing.T) {
	t.Parallel()

	idp := newTestIdentityProvider(t)
	projectID := uuid.New()
	botAppID := "bot-app-1"

	h := newTestHarness(t, idp, projectID, botAppID)

	convID := uuid.New()
	authorID := uuid.New()
	h.provider.inbound = service.InboundMessage{ConversationID: convID, AuthorMemberID: authorID, Body: "here's my answer"}
	h.followUp.err = errFakeNotFound

	tok := idp.token(t, "https://api.botframework.com", botAppID, time.Hour)

	body := []byte(`{"type":"message","text":"here's my answer","conversation":{"id":"conv-1"}}`)
	rec := doPost(h, "/teams/activities/"+projectID.String(), tok, body)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500 for a FollowUp dispatch failure (body: %s)", rec.Code, rec.Body.String())
	}

	if h.followUp.last.ConversationID != convID {
		t.Errorf("FollowUp ConversationID: got %v, want %v", h.followUp.last.ConversationID, convID)
	}
}

// TestServeActivities_UnrecognizedMessageAcks proves ErrUnrecognizedMessage
// from ParseInbound still returns 200 (ack, no retry) without dispatching.
func TestServeActivities_UnrecognizedMessageAcks(t *testing.T) {
	t.Parallel()

	idp := newTestIdentityProvider(t)
	projectID := uuid.New()
	botAppID := "bot-app-1"

	h := newTestHarness(t, idp, projectID, botAppID)
	h.provider.inboundErr = service.ErrUnrecognizedMessage

	tok := idp.token(t, "https://api.botframework.com", botAppID, time.Hour)

	rec := doPost(h, "/teams/activities/"+projectID.String(), tok, []byte(`{"type":"message"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}

	if h.followUp.calls != 0 {
		t.Errorf("FollowUp must not be dispatched for an unrecognized message, calls=%d", h.followUp.calls)
	}
}

// TestServeActivities_RejectsInvalidTokens proves every JWT validation
// failure mode returns 401: wrong audience, wrong issuer, bad signature,
// expired token, and a missing Authorization header.
func TestServeActivities_RejectsInvalidTokens(t *testing.T) {
	t.Parallel()

	idp := newTestIdentityProvider(t)
	projectID := uuid.New()
	botAppID := "bot-app-1"

	cases := []struct {
		name   string
		bearer func(t *testing.T) string
	}{
		{name: "wrong_audience", bearer: func(t *testing.T) string {
			t.Helper()
			return idp.token(t, "https://api.botframework.com", "some-other-bot-app-id", time.Hour)
		}},
		{name: "wrong_issuer", bearer: func(t *testing.T) string {
			t.Helper()
			return idp.token(t, "https://evil.example.com", botAppID, time.Hour)
		}},
		{name: "expired", bearer: func(t *testing.T) string {
			t.Helper()
			return idp.token(t, "https://api.botframework.com", botAppID, -time.Hour)
		}},
		{name: "forged_signature", bearer: func(t *testing.T) string {
			t.Helper()
			return idp.tokenSignedByOtherKey(t, "https://api.botframework.com", botAppID)
		}},
		{name: "missing_header", bearer: func(t *testing.T) string {
			t.Helper()
			return ""
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newTestHarness(t, idp, projectID, botAppID)

			bearer := tc.bearer(t)

			var rec *httptest.ResponseRecorder
			if bearer == "" {
				req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/teams/activities/"+projectID.String(), bytes.NewReader([]byte(`{}`)))
				rec = httptest.NewRecorder()
				h.router.ServeHTTP(rec, req)
			} else {
				rec = doPost(h, "/teams/activities/"+projectID.String(), bearer, []byte(`{}`))
			}

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status: got %d, want 401", rec.Code)
			}

			if h.followUp.calls != 0 {
				t.Errorf("no dispatch must happen on a rejected token: followup calls=%d", h.followUp.calls)
			}
		})
	}
}

// TestServeActivities_OverCapBodyRejected proves an over-cap Activity body is
// rejected with 413 rather than read into memory unbounded (review finding
// #9). The JWT is otherwise valid, isolating the size cap from token
// verification.
func TestServeActivities_OverCapBodyRejected(t *testing.T) {
	t.Parallel()

	idp := newTestIdentityProvider(t)
	projectID := uuid.New()
	botAppID := "bot-app-1"

	h := newTestHarness(t, idp, projectID, botAppID)

	tok := idp.token(t, "https://api.botframework.com", botAppID, time.Hour)

	oversized := bytes.Repeat([]byte("a"), (1<<20)+1)

	rec := doPost(h, "/teams/activities/"+projectID.String(), tok, oversized)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: got %d, want 413 (body: %s)", rec.Code, rec.Body.String())
	}

	if h.followUp.calls != 0 {
		t.Errorf("no dispatch must happen on an over-cap body: followup calls=%d", h.followUp.calls)
	}
}

// TestServeActivities_UnknownProjectReturns404 proves a project with no
// stored Teams config returns 404 before any JWT work.
func TestServeActivities_UnknownProjectReturns404(t *testing.T) {
	t.Parallel()

	idp := newTestIdentityProvider(t)
	registry := newFakeRegistry()
	configs := newFakeConfigStore()

	handler, err := teamstransport.NewWebhookHandler(
		t.Context(),
		idp.server.URL+"/v1/.well-known/openidconfiguration",
		registry, configs, &fakeFollowUpper{}, discardLogger(),
	)
	if err != nil {
		t.Fatalf("NewWebhookHandler: %v", err)
	}

	r := chi.NewRouter()
	handler.RegisterRoutes(r)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/teams/activities/"+uuid.New().String(), bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

// TestNewWebhookHandler_MetadataDiscoveryFailure proves a broken metadata
// endpoint fails construction rather than deferring to first-request panics.
func TestNewWebhookHandler_MetadataDiscoveryFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	_, err := teamstransport.NewWebhookHandler(
		context.Background(), server.URL, newFakeRegistry(), newFakeConfigStore(), &fakeFollowUpper{}, discardLogger(),
	)
	if err == nil {
		t.Fatal("want error when metadata discovery fails, got nil")
	}
}
