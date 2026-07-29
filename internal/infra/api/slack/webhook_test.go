// SPDX-License-Identifier: AGPL-3.0-or-later

package slack_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/provider"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/service"
	slackhttp "github.com/orako-io/core/internal/infra/api/slack"
)

// ── fakes ─────────────────────────────────────────────────────────────────────

// fakeProviderLookup implements providerLookup for tests.
type fakeProviderLookup struct {
	projectID uuid.UUID
	prov      service.Provider
}

func (f *fakeProviderLookup) ForProjectKind(_ context.Context, id uuid.UUID, _ provider.ProviderKind) (service.Provider, error) {
	if id == f.projectID {
		return f.prov, nil
	}

	return nil, errors.New("no provider for project")
}

// fakeProvider implements service.Provider for tests.
type fakeProvider struct {
	inbound service.InboundMessage
	err     error
}

func (f *fakeProvider) Deliver(_ context.Context, _ service.OutboundMessage) (service.MessageRef, error) {
	return service.MessageRef{}, nil
}

func (f *fakeProvider) ParseInbound(_ context.Context, _ []byte) (service.InboundMessage, error) {
	return f.inbound, f.err
}

// fakeFollowUp implements followUpper and records invocations.
type fakeFollowUp struct {
	err      error
	received []command.FollowUpCommand
}

func (f *fakeFollowUp) Handle(_ context.Context, cmd command.FollowUpCommand) (command.FollowUpResult, error) {
	f.received = append(f.received, cmd)
	return command.FollowUpResult{Outcome: command.OutcomeAppended}, f.err
}

// ── helpers ───────────────────────────────────────────────────────────────────

const webhookTestSecret = "test_webhook_signing_secret"

// buildHandler constructs a WebhookHandler wired with the given fakes. The
// signing secret is served through the per-project resolver (the production
// path); the env fallback stays empty.
func buildHandler(lookup *fakeProviderLookup, fu *fakeFollowUp) *slackhttp.WebhookHandler {
	return slackhttp.NewWebhookHandler(
		fakeSecretResolver{secret: webhookTestSecret},
		"",
		lookup,
		fu,
		newTestLogger(),
	)
}

// fakeSecretResolver serves one stored signing secret for every project.
type fakeSecretResolver struct{ secret string }

func (f fakeSecretResolver) LoadProvider(context.Context, uuid.UUID, string) ([]byte, error) {
	return []byte(`{"bot_token":"xoxb-test","signing_secret":"` + f.secret + `"}`), nil
}

// serveEvents routes req through a chi router so chi.URLParam finds the
// {projectID} path parameter (populated only via chi routing).
func serveEvents(handler *slackhttp.WebhookHandler, req *http.Request) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	handler.RegisterRoutes(router)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	return w
}

// postSlackEvent signs and posts body to the handler as a Slack Events API call.
// nowOverride allows injecting a fixed time so replay-window tests are stable.
func postSlackEvent(
	handler *slackhttp.WebhookHandler,
	projectID uuid.UUID,
	body []byte,
	nowOverride time.Time,
) *httptest.ResponseRecorder {
	ts, sig := signForTest(webhookTestSecret, body, nowOverride)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/slack/events/"+projectID.String(), bytes.NewReader(body))
	req.Header.Set("X-Slack-Signature", sig)
	req.Header.Set("X-Slack-Request-Timestamp", ts)

	return serveEvents(handler, req)
}

// postSlackEventUnsigned posts without any signature headers.
func postSlackEventUnsigned(handler *slackhttp.WebhookHandler, projectID uuid.UUID, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/slack/events/"+projectID.String(), bytes.NewReader(body))

	return serveEvents(handler, req)
}

// ── tests ──────────────────────────────────────────────────────────────────────

func TestWebhook_SignedValidPayload_DispatchesFollowUp(t *testing.T) {
	t.Parallel()

	convID := uuid.New()
	authorID := uuid.New()
	projectID := uuid.New()

	inbound := service.InboundMessage{
		ConversationID: convID,
		AuthorMemberID: authorID,
		Body:           "responder answer",
	}

	lookup := &fakeProviderLookup{
		projectID: projectID,
		prov:      &fakeProvider{inbound: inbound},
	}
	fu := &fakeFollowUp{}

	handler := buildHandler(lookup, fu)

	body := slackEventPayload(t, "Ev001", "event_callback", "here is my answer", "U001", "C001", "111.001", "111.000")
	w := postSlackEvent(handler, projectID, body, time.Now())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	if len(fu.received) != 1 {
		t.Fatalf("followUp called %d times, want 1", len(fu.received))
	}

	if fu.received[0].ConversationID != convID {
		t.Errorf("ConversationID: got %v, want %v", fu.received[0].ConversationID, convID)
	}
}

func TestWebhook_UnsignedRequest_Returns401(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	lookup := &fakeProviderLookup{
		projectID: projectID,
		prov:      &fakeProvider{},
	}
	fu := &fakeFollowUp{}

	handler := buildHandler(lookup, fu)
	w := postSlackEventUnsigned(handler, projectID, []byte(`{"type":"event_callback"}`))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for unsigned request", w.Code)
	}

	if len(fu.received) != 0 {
		t.Error("followUp must not be called for unsigned requests")
	}
}

// TestWebhook_OverCapBodyRejectedBeforeSignatureCheck proves an over-cap
// request body is rejected with 413 before signature verification even runs
// (an invalid signature header is present, yet the response is 413, not
// 401) — review finding #9 (unbounded io.ReadAll before the signature
// check), applied to the Slack Events webhook alongside interactivity.
func TestWebhook_OverCapBodyRejectedBeforeSignatureCheck(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	lookup := &fakeProviderLookup{
		projectID: projectID,
		prov:      &fakeProvider{},
	}
	fu := &fakeFollowUp{}

	handler := buildHandler(lookup, fu)

	oversized := bytes.Repeat([]byte("a"), (1<<20)+1)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/slack/events/"+projectID.String(), bytes.NewReader(oversized))
	req.Header.Set("X-Slack-Signature", "v0=badsignature")
	req.Header.Set("X-Slack-Request-Timestamp", "1234567890")

	w := serveEvents(handler, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}

	if len(fu.received) != 0 {
		t.Error("an over-cap body must never reach FollowUp dispatch")
	}
}

func TestWebhook_BadSignature_Returns401(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	lookup := &fakeProviderLookup{
		projectID: projectID,
		prov:      &fakeProvider{},
	}
	fu := &fakeFollowUp{}

	handler := buildHandler(lookup, fu)

	body := []byte(`{"type":"event_callback"}`)
	ts, _ := signForTest(webhookTestSecret, body, time.Now())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/slack/events/"+projectID.String(), bytes.NewReader(body))
	req.Header.Set("X-Slack-Signature", "v0=badsignature")
	req.Header.Set("X-Slack-Request-Timestamp", ts)

	w := serveEvents(handler, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for bad signature", w.Code)
	}
}

func TestWebhook_StaleTimestamp_Returns401(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	lookup := &fakeProviderLookup{
		projectID: projectID,
		prov:      &fakeProvider{},
	}
	fu := &fakeFollowUp{}

	handler := buildHandler(lookup, fu)

	body := []byte(`{"type":"event_callback"}`)
	// Compute a valid signature over a stale timestamp.
	staleTime := time.Now().Add(-6 * time.Minute) // 6 min old — outside the 5 min window
	ts, sig := signForTest(webhookTestSecret, body, staleTime)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/slack/events/"+projectID.String(), bytes.NewReader(body))
	req.Header.Set("X-Slack-Signature", sig)
	req.Header.Set("X-Slack-Request-Timestamp", ts)

	w := serveEvents(handler, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for stale timestamp", w.Code)
	}
}

func TestWebhook_URLVerificationChallenge_EchoesChallenge(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	lookup := &fakeProviderLookup{
		projectID: projectID,
		prov:      &fakeProvider{},
	}
	fu := &fakeFollowUp{}

	handler := buildHandler(lookup, fu)

	challenge := "3eZbrw1aBm2rZgRNFdxV2595E9CY3gmdALWMmHkvFXO7tYXAYM8P"
	body, _ := json.Marshal(map[string]string{
		"type":      "url_verification",
		"token":     "Jhj5dZrVaK7ZwHHjRyZEjbDjd",
		"challenge": challenge,
	})

	w := postSlackEvent(handler, projectID, body, time.Now())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for url_verification", w.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	if resp["challenge"] != challenge {
		t.Errorf("challenge: got %q, want %q", resp["challenge"], challenge)
	}

	// FollowUp must not be called for url_verification.
	if len(fu.received) != 0 {
		t.Error("followUp must not be called for url_verification")
	}
}

func TestWebhook_UnrecognizedMessage_Returns200(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	lookup := &fakeProviderLookup{
		projectID: projectID,
		prov:      &fakeProvider{err: service.ErrUnrecognizedMessage},
	}
	fu := &fakeFollowUp{}

	handler := buildHandler(lookup, fu)

	body := []byte(`{"type":"event_callback","event":{"type":"reaction_added"}}`)
	w := postSlackEvent(handler, projectID, body, time.Now())

	// Unrecognized messages must ack 200 (Slack retries on non-2xx).
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for unrecognized message", w.Code)
	}
}

func TestWebhook_DuplicateEventID_NotDoublePosted(t *testing.T) {
	t.Parallel()

	convID := uuid.New()
	authorID := uuid.New()
	projectID := uuid.New()

	inbound := service.InboundMessage{
		ConversationID: convID,
		AuthorMemberID: authorID,
		Body:           "answer",
	}

	lookup := &fakeProviderLookup{
		projectID: projectID,
		prov:      &fakeProvider{inbound: inbound},
	}
	fu := &fakeFollowUp{}

	handler := buildHandler(lookup, fu)

	// Both posts carry the same event_id.
	body := slackEventPayload(t, "EvDUPE", "event_callback", "answer", "U001", "C001", "111.002", "111.001")
	now := time.Now()

	w1 := postSlackEvent(handler, projectID, body, now)
	w2 := postSlackEvent(handler, projectID, body, now)

	if w1.Code != http.StatusOK {
		t.Fatalf("first post: status = %d, want 200", w1.Code)
	}

	if w2.Code != http.StatusOK {
		t.Fatalf("second post: status = %d, want 200", w2.Code)
	}

	if len(fu.received) != 1 {
		t.Fatalf("followUp called %d times, want 1 (dedup should suppress second)", len(fu.received))
	}
}

// TestWebhook_FollowUpFailure_Returns500 proves a real dispatch failure is
// surfaced as 500 so Slack retries the delivery — a reply is a message
// (hub-and-spoke phase 2), there is no claim rejection or second-opinion arm
// to translate into a special ack anymore.
func TestWebhook_FollowUpFailure_Returns500(t *testing.T) {
	t.Parallel()

	convID := uuid.New()
	authorID := uuid.New()
	projectID := uuid.New()

	inbound := service.InboundMessage{
		ConversationID: convID,
		AuthorMemberID: authorID,
		Body:           "here is my answer",
	}

	prov := &fakeProvider{inbound: inbound}
	lookup := &fakeProviderLookup{projectID: projectID, prov: prov}
	fu := &fakeFollowUp{err: errors.New("boom")}

	handler := buildHandler(lookup, fu)

	body := slackEventPayload(t, "EvFAIL", "event_callback", "here is my answer", "U002", "C002", "111.003", "111.000")
	w := postSlackEvent(handler, projectID, body, time.Now())

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for a FollowUp dispatch failure", w.Code)
	}

	if len(fu.received) != 1 {
		t.Fatalf("followUp called %d times, want 1", len(fu.received))
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// slackEventPayload builds a minimal Slack Events API event_callback payload.
func slackEventPayload(
	t *testing.T,
	eventID, evType, text, user, channel, ts, threadTs string,
) []byte {
	t.Helper()

	payload := map[string]any{
		"type":     evType,
		"event_id": eventID,
		"event": map[string]any{
			"type":      "message",
			"channel":   channel,
			"user":      user,
			"text":      text,
			"ts":        ts,
			"thread_ts": threadTs,
		},
	}

	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal slack event: %v", err)
	}

	return b
}

// errSecretResolver has no stored credentials for any project.
type errSecretResolver struct{}

func (errSecretResolver) LoadProvider(context.Context, uuid.UUID, string) ([]byte, error) {
	return nil, errors.New("not configured")
}

// TestWebhook_NoSigningSecretConfigured proves a project with no stored Slack
// credentials and no env fallback is answered 404 — never verified against an
// empty secret.
func TestWebhook_NoSigningSecretConfigured(t *testing.T) {
	t.Parallel()

	handler := slackhttp.NewWebhookHandler(errSecretResolver{}, "", &fakeProviderLookup{}, &fakeFollowUp{}, newTestLogger())

	if w := postSlackEvent(handler, uuid.New(), []byte(`{"type":"event_callback"}`), time.Now()); w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when slack is not configured for the project", w.Code)
	}
}
