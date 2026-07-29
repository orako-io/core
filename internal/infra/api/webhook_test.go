// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/provider"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/infra/api/inbound"
)

// fakeProviderLookup implements providerLookup for webhook tests. wantKind,
// when non-empty, additionally asserts the exact kind ServeWebhook resolved
// with — proving the handler reads the {provider} path segment rather than
// treating "any provider of the project" as good enough (review fit#4).
type fakeProviderLookup struct {
	projectID uuid.UUID
	wantKind  provider.ProviderKind
	gotKind   provider.ProviderKind
	prov      service.Provider
}

func (f *fakeProviderLookup) ForProjectKind(_ context.Context, id uuid.UUID, kind provider.ProviderKind) (service.Provider, error) {
	f.gotKind = kind

	if id != f.projectID {
		return nil, errors.New("no provider for project")
	}

	if f.wantKind != "" && kind != f.wantKind {
		return nil, errors.New("no provider for kind")
	}

	return f.prov, nil
}

// testWebhookSecret is the Telegram secret_token the test harness configures
// and sends, so authenticated requests pass verifyTelegramSecret.
const testWebhookSecret = "test-webhook-secret"

// buildWebhookHandler constructs a WebhookHandler wired with the given fakes and
// the test secret.
func buildWebhookHandler(lookup providerLookup, followUp followUpper) *WebhookHandler {
	return NewWebhookHandler(lookup, followUp, testWebhookSecret, nil, newTestLogger())
}

// postWebhook sends a POST through a chi router so the {provider}/{projectID}
// path params are populated (chi.URLParam reads the route context). Uses
// "slack" as the {provider} path segment — a placeholder for tests that don't
// care which kind is requested; use postWebhookKind to pin a specific one.
func postWebhook(handler *WebhookHandler, projectID uuid.UUID, body []byte) *httptest.ResponseRecorder {
	return postWebhookKind(handler, "slack", projectID, body)
}

// postWebhookKind is postWebhook with an explicit {provider} path segment.
func postWebhookKind(handler *WebhookHandler, providerKind string, projectID uuid.UUID, body []byte) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	handler.RegisterRoutes(router)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook/"+providerKind+"/"+projectID.String(), bytes.NewReader(body))
	req.Header.Set(telegramSecretHeader, testWebhookSecret)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	return w
}

// TestWebhook_MissingOrWrongSecret_Returns401 proves the generic route rejects
// an unauthenticated (or wrong-secret) request before resolving a provider or
// reading the body — the fix for the forge-a-reply finding.
func TestWebhook_MissingOrWrongSecret_Returns401(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	handler := buildWebhookHandler(&fakeProviderLookup{projectID: projectID, prov: &fakeProvider{}}, &fakeFollowUp{})

	router := chi.NewRouter()
	handler.RegisterRoutes(router)

	for _, tc := range []struct {
		name   string
		secret string
		set    bool
	}{
		{name: "no header", set: false},
		{name: "wrong secret", secret: "nope", set: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook/telegram/"+projectID.String(), bytes.NewReader([]byte(`{}`)))
			if tc.set {
				req.Header.Set(telegramSecretHeader, tc.secret)
			}

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", w.Code)
			}
		})
	}
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestWebhook_ParseInbound_DispatchesFollowUp(t *testing.T) {
	t.Parallel()

	convID := uuid.New()
	authorID := uuid.New()
	projectID := uuid.New()

	inbound := service.InboundMessage{
		ConversationID: convID,
		AuthorMemberID: authorID,
		Body:           "here is my answer",
	}

	lookup := &fakeProviderLookup{
		projectID: projectID,
		prov:      &fakeProvider{inbound: inbound},
	}
	fuHandler := &fakeFollowUp{}

	handler := buildWebhookHandler(lookup, fuHandler)
	w := postWebhook(handler, projectID, []byte(`{"event":"message"}`))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestWebhook_UnrecognizedMessage_Returns200(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()

	lookup := &fakeProviderLookup{
		projectID: projectID,
		prov:      &fakeProvider{err: service.ErrUnrecognizedMessage},
	}
	fuHandler := &fakeFollowUp{}

	handler := buildWebhookHandler(lookup, fuHandler)
	w := postWebhook(handler, projectID, []byte(`{"event":"bot_message"}`))

	// Unrecognized messages must ack with 200 (Slack retries on non-2xx).
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for unrecognized message", w.Code)
	}
}

func TestWebhook_ParseError_Returns400(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()

	lookup := &fakeProviderLookup{
		projectID: projectID,
		prov:      &fakeProvider{err: errors.New("malformed payload")},
	}
	fuHandler := &fakeFollowUp{}

	handler := buildWebhookHandler(lookup, fuHandler)
	w := postWebhook(handler, projectID, []byte(`not json`))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for parse error", w.Code)
	}
}

func TestWebhook_NoProviderForProject_Returns404(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	otherProject := uuid.New()

	lookup := &fakeProviderLookup{
		projectID: otherProject, // different project → ForProject will return error
		prov:      &fakeProvider{},
	}
	fuHandler := &fakeFollowUp{}

	handler := buildWebhookHandler(lookup, fuHandler)
	w := postWebhook(handler, projectID, []byte(`{}`))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for missing provider", w.Code)
	}
}

// TestWebhook_TeamsProvider_Returns410 proves the generic route rejects
// provider=="teams" — that traffic must go through the JWT-verified
// /teams/activities/{projectID} route instead. Telegram (and any other
// generic-route provider) must be unaffected.
func TestWebhook_TeamsProvider_Returns410(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()

	lookup := &fakeProviderLookup{projectID: projectID, prov: &fakeProvider{}}
	fuHandler := &fakeFollowUp{}

	handler := buildWebhookHandler(lookup, fuHandler)

	router := chi.NewRouter()
	handler.RegisterRoutes(router)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook/teams/"+projectID.String(), bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410 for provider=teams", w.Code)
	}
}

// TestWebhook_ResolvesRequestedKindNotJustProject proves ServeWebhook resolves
// the provider by the exact {provider} path segment, not merely "a provider
// configured for this project" — the review fit#4 scenario: a project with
// both Discord and Telegram configured must route a Discord-path request to
// the Discord provider even though the fake would happily return a
// project-only match for the wrong kind.
func TestWebhook_ResolvesRequestedKindNotJustProject(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()

	inbound := service.InboundMessage{
		ConversationID: uuid.New(),
		AuthorMemberID: uuid.New(),
		Body:           "discord reply",
	}

	lookup := &fakeProviderLookup{
		projectID: projectID,
		wantKind:  provider.ProviderKindDiscord,
		prov:      &fakeProvider{inbound: inbound},
	}
	fuHandler := &fakeFollowUp{}

	handler := buildWebhookHandler(lookup, fuHandler)
	w := postWebhookKind(handler, "discord", projectID, []byte(`{}`))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	if lookup.gotKind != provider.ProviderKindDiscord {
		t.Errorf("resolved kind = %q, want %q", lookup.gotKind, provider.ProviderKindDiscord)
	}

	// The same project requested under a different kind's path (Telegram) must
	// miss — proving the handler does not fall back to "any kind this project has".
	w2 := postWebhookKind(handler, "telegram", projectID, []byte(`{}`))
	if w2.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for the wrong kind on the same project", w2.Code)
	}
}

// stubUploader records the inbound attachment bytes it received and returns a
// fixed id, standing in for the real UploadAttachmentHandler.
type stubUploader struct {
	gotContent []byte
	gotName    string
	id         uuid.UUID
	err        error
}

func (s *stubUploader) Handle(_ context.Context, cmd command.UploadAttachmentCommand) (command.UploadAttachmentResult, error) {
	s.gotContent = cmd.Content
	s.gotName = cmd.Filename

	if s.err != nil {
		return command.UploadAttachmentResult{}, s.err
	}

	return command.UploadAttachmentResult{AttachmentID: s.id}, nil
}

// buildWebhookWithUploader wires a handler with a shared ingestor built over
// the given uploader and HTTP client. Tests pass the TLS test server's own
// client (trusts its cert, reaches loopback) since production uses an
// SSRF-guarded https-only client that would refuse a plaintext loopback URL.
func buildWebhookWithUploader(lookup providerLookup, followUp followUpper, up *stubUploader, maxBytes int64, client *http.Client) *WebhookHandler {
	ing := inbound.NewIngestor(up, client, maxBytes, newTestLogger())

	return NewWebhookHandler(lookup, followUp, testWebhookSecret, ing, newTestLogger())
}

// TestWebhook_InboundAttachment_DownloadedStoredAndLinked proves an inbound
// message carrying an attachment gets its bytes fetched from the token-bearing
// FetchURL, stored via the uploader, and the resulting id threaded into the
// FollowUp command that appends the reply.
func TestWebhook_InboundAttachment_DownloadedStoredAndLinked(t *testing.T) {
	t.Parallel()

	const fileBytes = "PNGDATA-inbound"

	files := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fileBytes))
	}))
	defer files.Close()

	projectID := uuid.New()
	attID := uuid.New()

	inbound := service.InboundMessage{
		ConversationID: uuid.New(),
		AuthorMemberID: uuid.New(),
		Body:           "with a screenshot",
		Attachments: []service.InboundAttachment{{
			Filename: "shot.png", MimeType: "image/png", SizeBytes: int64(len(fileBytes)),
			FetchURL: files.URL + "/file/bot-secret/photos/shot.png",
		}},
	}

	lookup := &fakeProviderLookup{projectID: projectID, prov: &fakeProvider{inbound: inbound}}
	fuHandler := &fakeFollowUp{}
	up := &stubUploader{id: attID}

	handler := buildWebhookWithUploader(lookup, fuHandler, up, 25<<20, files.Client())
	w := postWebhook(handler, projectID, []byte(`{"message":{"photo":[]}}`))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	if string(up.gotContent) != fileBytes || up.gotName != "shot.png" {
		t.Fatalf("uploader got %q/%q, want %q/shot.png", up.gotContent, up.gotName, fileBytes)
	}

	if len(fuHandler.last.AttachmentIDs) != 1 || fuHandler.last.AttachmentIDs[0] != attID {
		t.Fatalf("FollowUp AttachmentIDs = %v, want [%s]", fuHandler.last.AttachmentIDs, attID)
	}
}

// TestWebhook_InboundAttachment_FailureStillDeliversText proves a failed
// attachment download never blocks the text reply: FollowUp still fires, just
// with no attachment ids.
func TestWebhook_InboundAttachment_FailureStillDeliversText(t *testing.T) {
	t.Parallel()

	files := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer files.Close()

	projectID := uuid.New()

	inbound := service.InboundMessage{
		ConversationID: uuid.New(),
		AuthorMemberID: uuid.New(),
		Body:           "text survives",
		Attachments: []service.InboundAttachment{{
			Filename: "x.png", MimeType: "image/png",
			FetchURL: files.URL + "/file/bot-secret/x.png",
		}},
	}

	lookup := &fakeProviderLookup{projectID: projectID, prov: &fakeProvider{inbound: inbound}}
	fuHandler := &fakeFollowUp{}
	up := &stubUploader{id: uuid.New()}

	handler := buildWebhookWithUploader(lookup, fuHandler, up, 25<<20, files.Client())
	w := postWebhook(handler, projectID, []byte(`{}`))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (text must survive a failed attachment)", w.Code)
	}

	if fuHandler.last.Message != "text survives" || len(fuHandler.last.AttachmentIDs) != 0 {
		t.Fatalf("FollowUp = %q / ids=%v, want text-only", fuHandler.last.Message, fuHandler.last.AttachmentIDs)
	}
}

func TestWebhook_FollowUpDispatch_ReceivesInboundMessage(t *testing.T) {
	t.Parallel()

	convID := uuid.New()
	authorID := uuid.New()
	projectID := uuid.New()

	inbound := service.InboundMessage{
		ConversationID: convID,
		AuthorMemberID: authorID,
		Body:           "responder reply",
	}

	lookup := &fakeProviderLookup{
		projectID: projectID,
		prov:      &fakeProvider{inbound: inbound},
	}
	fuHandler := &fakeFollowUp{}

	handler := buildWebhookHandler(lookup, fuHandler)
	w := postWebhook(handler, projectID, []byte(`{"text":"responder reply"}`))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}
