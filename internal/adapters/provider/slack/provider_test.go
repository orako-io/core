// SPDX-License-Identifier: AGPL-3.0-or-later

package slack_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/provider/slack"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

// slackBody is the subset of a Slack chat.postMessage/chat.update request
// body tests need to assert on.
type slackBody struct {
	Channel string `json:"channel"`
	Ts      string `json:"ts"`
	Text    string `json:"text"`
	Blocks  []struct {
		Type     string `json:"type"`
		Elements []struct {
			Type     string `json:"type"`
			ActionID string `json:"action_id"`
			Value    string `json:"value"`
		} `json:"elements"`
	} `json:"blocks"`
}

// ── Deliver ──────────────────────────────────────────────────────────────────

// TestProviderDeliver_PostsToSlackAndRecordsThread verifies that Deliver:
//   - resolves the responder's slack_user_id from their member record
//   - sends a well-formed chat.postMessage request (correct path, auth header, body)
//   - records the returned channel/ts on the conversation via SetSlackThread
//     (the direct-ask path only)
//   - returns the channel/ts as a MessageRef
func TestProviderDeliver_PostsToSlackAndRecordsThread(t *testing.T) {
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

			var captured slackBody

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/chat.postMessage" {
					t.Errorf("path: got %q, want /chat.postMessage", r.URL.Path)
				}

				if got := r.Header.Get("Authorization"); got != "Bearer xoxb-test" {
					t.Errorf("Authorization: got %q, want %q", got, "Bearer xoxb-test")
				}

				if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
					t.Errorf("decoding request body: %v", err)
				}

				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true,"channel":"DDMCHANNEL","ts":"1111111111.000001"}`))
			}))
			defer srv.Close()

			members := newFakeMemberStore()
			members.add(model.Member{ID: targetID, SlackUserID: "USPECIALIST"})
			convs := newFakeConvStore()

			provider := slack.New(slack.Config{
				BotToken: "xoxb-test",
				BaseURL:  srv.URL,
			}, members, convs, nil)

			msg := service.OutboundMessage{
				ConversationID:    convID,
				ResponderMemberID: targetID,
				Question:          tc.question,
				Context:           tc.context,
			}

			ref, err := provider.Deliver(context.Background(), msg)
			if err != nil {
				t.Fatalf("Deliver: %v", err)
			}

			if captured.Channel != "USPECIALIST" {
				t.Errorf("channel: got %q, want USPECIALIST", captured.Channel)
			}

			if !strings.Contains(captured.Text, tc.wantText) {
				t.Errorf("text: got %q, does not contain %q", captured.Text, tc.wantText)
			}

			if len(captured.Blocks) != 0 {
				t.Errorf("Deliver must not send blocks, got %d", len(captured.Blocks))
			}

			if ref.ChannelID != "DDMCHANNEL" || ref.MessageID != "1111111111.000001" {
				t.Errorf("MessageRef = %+v, want {DDMCHANNEL 1111111111.000001}", ref)
			}

			// Assert thread binding was recorded (direct-ask path: no RecipientMemberID).
			thread, ok := convs.threads[convID]
			if !ok {
				t.Fatal("SetSlackThread was not called")
			}

			if thread[0] != "DDMCHANNEL" {
				t.Errorf("slack_channel_id: got %q, want DDMCHANNEL", thread[0])
			}

			if thread[1] != "1111111111.000001" {
				t.Errorf("slack_thread_ts: got %q, want 1111111111.000001", thread[1])
			}
		})
	}
}

// TestProviderDeliver_PoolDMSendsPlainText verifies that a pool-DM Deliver
// (RecipientMemberID set) sends plain text with no interactive blocks (Claim
// buttons are retired) — and does NOT record conversations.slack_thread (that
// column stays for the direct-ask path only).
func TestProviderDeliver_PoolDMSendsPlainText(t *testing.T) {
	t.Parallel()

	candidateID := uuid.New()
	convID := uuid.New()

	var captured slackBody

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decoding request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"DPOOLCHAN","ts":"2222222222.000001"}`))
	}))
	defer srv.Close()

	members := newFakeMemberStore()
	members.add(model.Member{ID: candidateID, SlackUserID: "UCANDIDATE"})
	convs := newFakeConvStore()

	provider := slack.New(slack.Config{BotToken: "xoxb-test", BaseURL: srv.URL}, members, convs, nil)

	ref, err := provider.Deliver(context.Background(), service.OutboundMessage{
		ConversationID:    convID,
		RecipientMemberID: candidateID,
		Kind:              service.MessageKindQuestion,
		Question:          "Anyone know Postgres tuning?",
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	if ref.ChannelID != "DPOOLCHAN" || ref.MessageID != "2222222222.000001" {
		t.Errorf("MessageRef = %+v", ref)
	}

	if !strings.Contains(captured.Text, "Anyone know Postgres tuning?") {
		t.Errorf("text: got %q, does not contain the question", captured.Text)
	}

	if len(captured.Blocks) != 0 {
		t.Errorf("a pool-DM Deliver must not send blocks, got %d", len(captured.Blocks))
	}

	if _, ok := convs.threads[convID]; ok {
		t.Error("a pool-DM Deliver must NOT write conversations.slack_thread")
	}
}

// TestProviderDeliver_MissingSlackUserID verifies that Deliver returns an error
// when the responder has no Slack user ID bound.
func TestProviderDeliver_MissingSlackUserID(t *testing.T) {
	t.Parallel()

	targetID := uuid.New()
	members := newFakeMemberStore()
	members.add(model.Member{ID: targetID, SlackUserID: ""}) // no slack binding

	provider := slack.New(slack.Config{BotToken: "xoxb-test"}, members, newFakeConvStore(), nil)

	_, err := provider.Deliver(context.Background(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: targetID,
		Question:          "X?",
	})

	if err == nil {
		t.Fatal("want error for missing Slack user ID, got nil")
	}
}

// TestProviderDeliver_SlackAPIError verifies that a non-ok Slack API response
// is surfaced as an error.
func TestProviderDeliver_SlackAPIError(t *testing.T) {
	t.Parallel()

	targetID := uuid.New()
	members := newFakeMemberStore()
	members.add(model.Member{ID: targetID, SlackUserID: "USPECIALIST"})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"channel_not_found"}`))
	}))
	defer srv.Close()

	provider := slack.New(slack.Config{BotToken: "xoxb-test", BaseURL: srv.URL}, members, newFakeConvStore(), nil)

	_, err := provider.Deliver(context.Background(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: targetID,
		Question:          "X?",
	})

	if err == nil {
		t.Fatal("want error for Slack API error, got nil")
	}

	if !strings.Contains(err.Error(), "channel_not_found") {
		t.Errorf("error should mention API error code, got: %v", err)
	}
}

// TestProviderDeliver_KindVariesRendering proves rot#6's minimal, real use of
// MessageKind: a nudge or closure Deliver renders with a distinct prefix from
// the default question rendering, so the field is genuinely consumed rather
// than written everywhere and read nowhere.
func TestProviderDeliver_KindVariesRendering(t *testing.T) {
	t.Parallel()

	targetID := uuid.New()

	cases := []struct {
		name       string
		kind       service.MessageKind
		wantPrefix string
	}{
		{name: "question", kind: service.MessageKindQuestion, wantPrefix: "[orako]"},
		{name: "unset_kind", kind: "", wantPrefix: "[orako]"},
		{name: "nudge", kind: service.MessageKindNudge, wantPrefix: "🔔 Reminder: [orako]"},
		{name: "closure", kind: service.MessageKindClosure, wantPrefix: "📌 [orako]"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var captured slackBody

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
					t.Errorf("decoding request body: %v", err)
				}

				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true,"channel":"DDMCHANNEL","ts":"1111111111.000002"}`))
			}))
			defer srv.Close()

			members := newFakeMemberStore()
			members.add(model.Member{ID: targetID, SlackUserID: "USPECIALIST"})

			provider := slack.New(slack.Config{BotToken: "xoxb-test", BaseURL: srv.URL}, members, newFakeConvStore(), nil)

			_, err := provider.Deliver(context.Background(), service.OutboundMessage{
				ConversationID:    uuid.New(),
				ResponderMemberID: targetID,
				Kind:              tc.kind,
				Question:          "Still waiting?",
			})
			if err != nil {
				t.Fatalf("Deliver: %v", err)
			}

			if !strings.HasPrefix(captured.Text, tc.wantPrefix) {
				t.Errorf("text = %q, want prefix %q", captured.Text, tc.wantPrefix)
			}
		})
	}
}

// ── Editor ───────────────────────────────────────────────────────────────────

// TestProviderEdit_UpdatesTextWithNoBlocks verifies Edit calls chat.update
// with the new text and never sends blocks — a legacy message delivered before
// the claim teardown may still carry a Claim actions block; the edit retires it.
func TestProviderEdit_UpdatesTextWithNoBlocks(t *testing.T) {
	t.Parallel()

	var captured slackBody

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.update" {
			t.Errorf("path: got %q, want /chat.update", r.URL.Path)
		}

		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decoding request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"DPOOLCHAN","ts":"2222222222.000001"}`))
	}))
	defer srv.Close()

	provider := slack.New(slack.Config{BotToken: "xoxb-test", BaseURL: srv.URL}, newFakeMemberStore(), newFakeConvStore(), nil)

	err := provider.Edit(context.Background(), service.MessageRef{ChannelID: "DPOOLCHAN", MessageID: "2222222222.000001"}, "✅ Answered by Sam")
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}

	if captured.Channel != "DPOOLCHAN" || captured.Ts != "2222222222.000001" {
		t.Errorf("channel/ts: got (%q, %q), want (DPOOLCHAN, 2222222222.000001)", captured.Channel, captured.Ts)
	}

	if captured.Text != "✅ Answered by Sam" {
		t.Errorf("text: got %q", captured.Text)
	}

	if len(captured.Blocks) != 0 {
		t.Errorf("Edit must send no blocks, got %d", len(captured.Blocks))
	}
}

// ── ChannelPoster ────────────────────────────────────────────────────────────

// TestProviderPostChannel_PostsToNamedChannel verifies PostChannel calls
// chat.postMessage against the given channel id with no interactive blocks.
func TestProviderPostChannel_PostsToNamedChannel(t *testing.T) {
	t.Parallel()

	var captured slackBody

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Errorf("path: got %q, want /chat.postMessage", r.URL.Path)
		}

		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decoding request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"CALERTS","ts":"3333333333.000001"}`))
	}))
	defer srv.Close()

	provider := slack.New(slack.Config{BotToken: "xoxb-test", BaseURL: srv.URL}, newFakeMemberStore(), newFakeConvStore(), nil)

	ref, err := provider.PostChannel(context.Background(), "CALERTS", "⏱ Still unclaimed after 4h")
	if err != nil {
		t.Fatalf("PostChannel: %v", err)
	}

	if captured.Channel != "CALERTS" {
		t.Errorf("channel: got %q, want CALERTS", captured.Channel)
	}

	if len(captured.Blocks) != 0 {
		t.Errorf("PostChannel must not send interactive blocks, got %d", len(captured.Blocks))
	}

	if ref.ChannelID != "CALERTS" || ref.MessageID != "3333333333.000001" {
		t.Errorf("MessageRef = %+v", ref)
	}
}
