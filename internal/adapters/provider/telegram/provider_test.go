// SPDX-License-Identifier: AGPL-3.0-or-later

package telegram_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/provider/telegram"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

// ── fakes ──────────────────────────────────────────────────────────────────────

type fakeMemberStore struct {
	byID             map[uuid.UUID]model.Member
	byTelegramChatID map[string]model.Member
}

func newFakeMemberStore() *fakeMemberStore {
	return &fakeMemberStore{
		byID:             make(map[uuid.UUID]model.Member),
		byTelegramChatID: make(map[string]model.Member),
	}
}

func (f *fakeMemberStore) add(m model.Member) {
	f.byID[m.ID] = m

	if m.TelegramChatID != "" {
		f.byTelegramChatID[m.TelegramChatID] = m
	}
}

func (f *fakeMemberStore) ByID(_ context.Context, id uuid.UUID) (model.Member, error) {
	m, ok := f.byID[id]
	if !ok {
		return model.Member{}, adaptererr.ErrNotFound
	}

	return m, nil
}

func (f *fakeMemberStore) ByTelegramChatID(_ context.Context, chatID string) (model.Member, error) {
	m, ok := f.byTelegramChatID[chatID]
	if !ok {
		return model.Member{}, adaptererr.ErrNotFound
	}

	return m, nil
}

// telegramMsgKey is the composite lookup key for fakeConvStore.
type telegramMsgKey struct {
	chatID    string
	messageID string
}

type fakeConvStore struct {
	byTelegramMsg map[telegramMsgKey]model.Conversation
	// threads records calls to SetTelegramThread; key is conversation ID.
	threads map[uuid.UUID][2]string
}

func newFakeConvStore() *fakeConvStore {
	return &fakeConvStore{
		byTelegramMsg: make(map[telegramMsgKey]model.Conversation),
		threads:       make(map[uuid.UUID][2]string),
	}
}

func (f *fakeConvStore) addConversation(chatID, messageID string, conv model.Conversation) {
	f.byTelegramMsg[telegramMsgKey{chatID, messageID}] = conv
}

func (f *fakeConvStore) ConversationByTelegramMessage(_ context.Context, chatID, messageID string) (model.Conversation, error) {
	c, ok := f.byTelegramMsg[telegramMsgKey{chatID, messageID}]
	if !ok {
		return model.Conversation{}, adaptererr.ErrNotFound
	}

	return c, nil
}

func (f *fakeConvStore) SetTelegramThread(_ context.Context, id uuid.UUID, chatID, messageID string) error {
	f.threads[id] = [2]string{chatID, messageID}

	return nil
}

// ── Deliver tests ──────────────────────────────────────────────────────────────

// TestProviderDeliver_PostsToTelegramAndRecordsThread verifies that Deliver:
//   - resolves the responder's telegram_chat_id from their member record
//   - sends a well-formed sendMessage request (correct path, chat_id, text)
//   - records the returned chat_id / message_id on the conversation via SetTelegramThread
func TestProviderDeliver_PostsToTelegramAndRecordsThread(t *testing.T) {
	t.Parallel()

	targetID := uuid.New()
	convID := uuid.New()

	cases := []struct {
		name     string
		question string
		context  string
		wantText string
	}{
		{
			name:     "question_only",
			question: "What is X?",
			context:  "",
			wantText: "What is X?",
		},
		{
			name:     "question_with_context",
			question: "Why does Y fail?",
			context:  "error logs attached",
			wantText: "Why does Y fail?",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var (
				capturedPath   string
				capturedChatID string
				capturedText   string
			)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedPath = r.URL.Path

				var body struct {
					ChatID string `json:"chat_id"`
					Text   string `json:"text"`
				}

				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decoding request body: %v", err)
				}

				capturedChatID = body.ChatID
				capturedText = body.Text

				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
			}))
			defer srv.Close()

			members := newFakeMemberStore()
			members.add(model.Member{ID: targetID, TelegramChatID: "123456789"})
			convs := newFakeConvStore()

			provider := telegram.New(telegram.Config{
				BotToken: "test-token",
				BaseURL:  srv.URL,
			}, members, convs)

			msg := service.OutboundMessage{
				ConversationID:    convID,
				ResponderMemberID: targetID,
				Question:          tc.question,
				Context:           tc.context,
			}

			if _, err := provider.Deliver(context.Background(), msg); err != nil {
				t.Fatalf("Deliver: %v", err)
			}

			// Assert request path contains the token and method name.
			wantPath := "/bottest-token/sendMessage"
			if capturedPath != wantPath {
				t.Errorf("path: got %q, want %q", capturedPath, wantPath)
			}

			if capturedChatID != "123456789" {
				t.Errorf("chat_id: got %q, want 123456789", capturedChatID)
			}

			if !strings.Contains(capturedText, tc.wantText) {
				t.Errorf("text: got %q, does not contain %q", capturedText, tc.wantText)
			}

			// Assert thread binding was recorded.
			thread, ok := convs.threads[convID]
			if !ok {
				t.Fatal("SetTelegramThread was not called")
			}

			if thread[0] != "123456789" {
				t.Errorf("telegram_chat_id: got %q, want 123456789", thread[0])
			}

			if thread[1] != "42" {
				t.Errorf("telegram_message_id: got %q, want 42", thread[1])
			}
		})
	}
}

// TestProviderDeliver_MissingTelegramChatID verifies that Deliver returns an error
// when the responder has no Telegram chat ID bound.
func TestProviderDeliver_MissingTelegramChatID(t *testing.T) {
	t.Parallel()

	targetID := uuid.New()
	members := newFakeMemberStore()
	members.add(model.Member{ID: targetID, TelegramChatID: ""}) // no telegram binding

	provider := telegram.New(telegram.Config{BotToken: "test-token"}, members, newFakeConvStore())

	_, err := provider.Deliver(context.Background(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: targetID,
		Question:          "X?",
	})

	if err == nil {
		t.Fatal("want error for missing Telegram chat ID, got nil")
	}
}

// TestProviderDeliver_TelegramAPIError verifies that a non-ok Telegram API response
// is surfaced as an error.
func TestProviderDeliver_TelegramAPIError(t *testing.T) {
	t.Parallel()

	targetID := uuid.New()
	members := newFakeMemberStore()
	members.add(model.Member{ID: targetID, TelegramChatID: "999"})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"description":"chat not found"}`))
	}))
	defer srv.Close()

	provider := telegram.New(telegram.Config{BotToken: "test-token", BaseURL: srv.URL}, members, newFakeConvStore())

	_, err := provider.Deliver(context.Background(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: targetID,
		Question:          "X?",
	})

	if err == nil {
		t.Fatal("want error for Telegram API error, got nil")
	}

	if !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("error should mention API error description, got: %v", err)
	}
}

// fakeFailingTransport always fails at the transport level (dial never
// reaches a server), simulating a network/timeout failure. http.Client.Do
// wraps this into a *url.Error whose Error() string embeds the full request
// URL — here ".../bot<TOKEN>/sendMessage" — so this is the seam that proves
// the bot token never survives into Deliver's returned error.
type fakeFailingTransport struct{}

func (fakeFailingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}

// TestProviderDeliver_TransportErrorDoesNotLeakBotToken proves a transport
// (network-level) failure never surfaces the bot-token-bearing request URL
// in Deliver's returned error — the exact leak path into
// application.fallbackAfterFailure's dashboard-visible binding_error.
func TestProviderDeliver_TransportErrorDoesNotLeakBotToken(t *testing.T) {
	t.Parallel()

	const fakeToken = "123456:FAKE-SECRET-TOKEN"

	targetID := uuid.New()
	members := newFakeMemberStore()
	members.add(model.Member{ID: targetID, TelegramChatID: "999"})

	provider := telegram.New(telegram.Config{
		BotToken:   fakeToken,
		HTTPClient: &http.Client{Transport: fakeFailingTransport{}},
	}, members, newFakeConvStore())

	_, err := provider.Deliver(context.Background(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: targetID,
		Question:          "X?",
	})

	if err == nil {
		t.Fatal("want error for transport failure, got nil")
	}

	if strings.Contains(err.Error(), fakeToken) {
		t.Errorf("error must not leak the bot token, got: %v", err)
	}

	if strings.Contains(err.Error(), "http://") || strings.Contains(err.Error(), "https://") {
		t.Errorf("error must not leak the request URL, got: %v", err)
	}
}

// ── ParseInbound tests ─────────────────────────────────────────────────────────

// TestParseInbound exercises the full inbound correlation path and the
// ErrUnrecognizedMessage cases.
func TestParseInbound(t *testing.T) {
	t.Parallel()

	convID := uuid.New()
	targetID := uuid.New()

	members := newFakeMemberStore()
	members.add(model.Member{ID: targetID, TelegramChatID: "123456789"})

	convs := newFakeConvStore()
	convs.addConversation("123456789", "50", model.Conversation{
		ID: convID,
	})

	newProvider := func() *telegram.Provider {
		return telegram.New(telegram.Config{BotToken: "test-token"}, members, convs)
	}

	cases := []struct {
		name         string
		payload      []byte
		wantConvID   uuid.UUID
		wantAuthorID uuid.UUID
		wantBody     string
		wantErr      error // nil = no error expected
	}{
		{
			name: "valid_reply",
			payload: []byte(`{
				"update_id": 1,
				"message": {
					"message_id": 100,
					"from": {"id": 123456789},
					"chat": {"id": 123456789},
					"text": "The answer is 42",
					"reply_to_message": {"message_id": 50, "chat": {"id": 123456789}}
				}
			}`),
			wantConvID:   convID,
			wantAuthorID: targetID,
			wantBody:     "The answer is 42",
		},
		{
			name:    "not_a_message_update",
			payload: []byte(`{"update_id": 2, "edited_message": {"text": "oops"}}`),
			wantErr: service.ErrUnrecognizedMessage,
		},
		{
			name: "no_reply_to_message",
			payload: []byte(`{
				"update_id": 3,
				"message": {
					"message_id": 200,
					"from": {"id": 123456789},
					"chat": {"id": 123456789},
					"text": "top-level message"
				}
			}`),
			wantErr: service.ErrUnrecognizedMessage,
		},
		{
			name: "unknown_reply_message",
			payload: []byte(`{
				"update_id": 4,
				"message": {
					"message_id": 300,
					"from": {"id": 123456789},
					"chat": {"id": 123456789},
					"text": "reply to unknown",
					"reply_to_message": {"message_id": 999, "chat": {"id": 123456789}}
				}
			}`),
			wantErr: adaptererr.ErrNotFound,
		},
		{
			name: "unknown_member",
			payload: []byte(`{
				"update_id": 5,
				"message": {
					"message_id": 400,
					"from": {"id": 999999999},
					"chat": {"id": 123456789},
					"text": "reply from unknown member",
					"reply_to_message": {"message_id": 50, "chat": {"id": 123456789}}
				}
			}`),
			wantErr: adaptererr.ErrNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := newProvider().ParseInbound(context.Background(), tc.payload)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error: got %v, want errors.Is(err, %v)", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseInbound: %v", err)
			}

			if got.ConversationID != tc.wantConvID {
				t.Errorf("ConversationID: got %v, want %v", got.ConversationID, tc.wantConvID)
			}

			if got.AuthorMemberID != tc.wantAuthorID {
				t.Errorf("AuthorMemberID: got %v, want %v", got.AuthorMemberID, tc.wantAuthorID)
			}

			if got.Body != tc.wantBody {
				t.Errorf("Body: got %q, want %q", got.Body, tc.wantBody)
			}
		})
	}
}

// TestProviderDeliver_SendsAttachmentsByURL proves an outbound message's
// attachments are delivered after the text: an image via sendPhoto, any other
// file via sendDocument, each carrying the signed URL Telegram fetches.
func TestProviderDeliver_SendsAttachmentsByURL(t *testing.T) {
	t.Parallel()

	targetID := uuid.New()

	type call struct {
		path  string
		field string // "photo" or "document"
		value string
	}

	var calls []call

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)

		c := call{path: r.URL.Path}
		if v, ok := body["photo"]; ok {
			c.field, c.value = "photo", v
		} else if v, ok := body["document"]; ok {
			c.field, c.value = "document", v
		}

		calls = append(calls, c)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()

	members := newFakeMemberStore()
	members.add(model.Member{ID: targetID, TelegramChatID: "123456789"})

	provider := telegram.New(telegram.Config{BotToken: "test-token", BaseURL: srv.URL}, members, newFakeConvStore())

	_, err := provider.Deliver(t.Context(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: targetID,
		Question:          "voici les fichiers",
		Attachments: []service.OutboundAttachment{
			{Filename: "shot.png", MimeType: "image/png", URL: "https://signed/shot.png"},
			{Filename: "logs.txt", MimeType: "text/plain", URL: "https://signed/logs.txt"},
		},
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	// sendMessage (text) + sendPhoto + sendDocument = 3 calls.
	var photo, doc *call

	for i := range calls {
		switch calls[i].field {
		case "photo":
			photo = &calls[i]
		case "document":
			doc = &calls[i]
		}
	}

	if photo == nil || photo.value != "https://signed/shot.png" {
		t.Errorf("image must be sent via sendPhoto by URL, got %+v", photo)
	}

	if doc == nil || doc.value != "https://signed/logs.txt" {
		t.Errorf("non-image must be sent via sendDocument by URL, got %+v", doc)
	}
}
