// SPDX-License-Identifier: AGPL-3.0-or-later

package teams_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/provider/teams"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

var (
	_ service.Provider      = (*teams.Provider)(nil)
	_ service.Editor        = (*teams.Provider)(nil)
	_ service.ChannelPoster = (*teams.Provider)(nil)
)

// fakeBotFramework fakes both the AAD v2 token endpoint and the Bot Framework
// connector REST API on a single httptest.Server. It counts token requests so
// tests can assert the token is cached, not refetched per call.
type fakeBotFramework struct {
	tokenRequests int
	conversations int
	activities    int
	lastActivity  map[string]any
}

func newFakeBotFramework(t *testing.T) (*httptest.Server, *fakeBotFramework) {
	t.Helper()

	fake := &fakeBotFramework{}

	mux := http.NewServeMux()

	mux.HandleFunc("/tenant-uuid/oauth2/v2.0/token", func(w http.ResponseWriter, _ *http.Request) {
		fake.tokenRequests++

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fake-connector-token",
			"expires_in":   3600,
		})
	})

	mux.HandleFunc("/v3/conversations", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		fake.conversations++

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "conv-1"})
	})

	// Handles both POST .../activities and PUT .../activities/{id}.
	mux.HandleFunc("/v3/conversations/conv-1/activities", func(w http.ResponseWriter, r *http.Request) {
		fake.activities++
		_ = json.NewDecoder(r.Body).Decode(&fake.lastActivity)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "activity-1"})
	})
	mux.HandleFunc("/v3/conversations/conv-1/activities/activity-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		fake.activities++
		_ = json.NewDecoder(r.Body).Decode(&fake.lastActivity)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "activity-1"})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server, fake
}

func testConfig(serverURL string) teams.Config {
	return teams.Config{
		TenantID:                  "tenant-uuid",
		ClientID:                  "client-uuid",
		ClientSecret:              "secret",
		BotAppID:                  "bot-uuid",
		ServiceURLOverrideForTest: serverURL,
		AADBaseURL:                serverURL,
		HTTPClient:                nil,
	}
}

// TestProvider_Deliver_PlainText proves Deliver creates a conversation, posts
// the message as a plain-text activity with no attachments (Claim Adaptive
// Cards are retired), and caches the AAD token across the two connector calls
// (create conversation + post activity).
func TestProvider_Deliver_PlainText(t *testing.T) {
	t.Parallel()

	server, fake := newFakeBotFramework(t)

	members := newFakeMemberStore()
	targetID := uuid.New()
	members.add(model.Member{ID: targetID, TeamsUserID: "aad-obj-1"})

	p := teams.New(testConfig(server.URL), members, nil)

	ref, err := p.Deliver(t.Context(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: targetID,
		Question:          "what's the timeout?",
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	if ref.ChannelID != "conv-1" || ref.MessageID != "activity-1" {
		t.Errorf("MessageRef: got %+v, want {conv-1 activity-1}", ref)
	}

	if fake.conversations != 1 {
		t.Errorf("conversations created: got %d, want 1", fake.conversations)
	}

	if fake.tokenRequests != 1 {
		t.Errorf("token requests: got %d, want 1 (cached across calls)", fake.tokenRequests)
	}

	if _, present := fake.lastActivity["attachments"]; present {
		t.Errorf("activity must not carry attachments, got %+v", fake.lastActivity)
	}
}

// TestProvider_Deliver_UnboundMember proves Deliver fails clearly when the
// recipient has no Teams binding.
func TestProvider_Deliver_UnboundMember(t *testing.T) {
	t.Parallel()

	server, _ := newFakeBotFramework(t)

	members := newFakeMemberStore()
	targetID := uuid.New()
	members.add(model.Member{ID: targetID, TeamsUserID: ""})

	p := teams.New(testConfig(server.URL), members, nil)

	_, err := p.Deliver(t.Context(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: targetID,
		Question:          "ping",
	})
	if err == nil {
		t.Fatal("want error for unbound member, got nil")
	}
}

// TestProvider_Edit_PlainTextNoAttachments proves Edit rewrites the activity
// as plain text with no attachments — a legacy activity delivered before the
// claim teardown may still carry a Claim Adaptive Card; the edit retires it.
func TestProvider_Edit_PlainTextNoAttachments(t *testing.T) {
	t.Parallel()

	server, fake := newFakeBotFramework(t)

	p := teams.New(testConfig(server.URL), newFakeMemberStore(), nil)

	err := p.Edit(t.Context(), service.MessageRef{ChannelID: "conv-1", MessageID: "activity-1"}, "closed")
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}

	if text, _ := fake.lastActivity["text"].(string); text != "closed" {
		t.Errorf("edited text: got %q, want %q", text, "closed")
	}

	if _, present := fake.lastActivity["attachments"]; present {
		t.Errorf("edit must not carry attachments, got %+v", fake.lastActivity)
	}
}

// TestProvider_PostChannel proves PostChannel posts to an existing
// conversation id without creating a new one.
func TestProvider_PostChannel(t *testing.T) {
	t.Parallel()

	server, fake := newFakeBotFramework(t)

	p := teams.New(testConfig(server.URL), newFakeMemberStore(), nil)

	ref, err := p.PostChannel(t.Context(), "conv-1", "resolution posted")
	if err != nil {
		t.Fatalf("PostChannel: %v", err)
	}

	if ref.ChannelID != "conv-1" {
		t.Errorf("ChannelID: got %q, want conv-1", ref.ChannelID)
	}

	if fake.conversations != 0 {
		t.Errorf("PostChannel must not create a new conversation, got %d creations", fake.conversations)
	}
}

// TestProvider_QuestionTextIncludesContext proves the plain-text fallback
// includes the context packet when present.
func TestProvider_QuestionTextIncludesContext(t *testing.T) {
	t.Parallel()

	server, fake := newFakeBotFramework(t)

	members := newFakeMemberStore()
	targetID := uuid.New()
	members.add(model.Member{ID: targetID, TeamsUserID: "aad-obj-1"})

	p := teams.New(testConfig(server.URL), members, nil)

	_, err := p.Deliver(t.Context(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: targetID,
		Question:          "what's the timeout?",
		Context:           "src/config.go:42",
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	text, _ := fake.lastActivity["text"].(string)
	if !strings.Contains(text, "src/config.go:42") {
		t.Errorf("activity text %q does not include context", text)
	}
}
