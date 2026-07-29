// SPDX-License-Identifier: AGPL-3.0-or-later

package slack_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/provider/slack"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

// TestParseInbound exercises the full inbound correlation path and the
// ErrUnrecognizedMessage cases.
func TestParseInbound(t *testing.T) {
	t.Parallel()

	convID := uuid.New()
	targetID := uuid.New()

	members := newFakeMemberStore()
	members.add(model.Member{ID: targetID, SlackUserID: "USPECIALIST"})

	convs := newFakeConvStore()
	convs.addConversation("DDMCHANNEL", "1111111111.000001", model.Conversation{
		ID: convID,
	})

	newProvider := func() *slack.Provider {
		return slack.New(slack.Config{BotToken: "xoxb-test"}, members, convs, newFakeLedger())
	}

	cases := []struct {
		name           string
		payload        []byte
		wantConvID     uuid.UUID
		wantAuthorID   uuid.UUID
		wantBody       string
		wantErr        error // nil = no error expected
		wantErrContain string
	}{
		{
			name: "valid_thread_reply",
			payload: []byte(`{
				"type": "event_callback",
				"event": {
					"type": "message",
					"channel": "DDMCHANNEL",
					"user": "USPECIALIST",
					"text": "Y is Z",
					"ts": "1111111111.000002",
					"thread_ts": "1111111111.000001"
				}
			}`),
			wantConvID:   convID,
			wantAuthorID: targetID,
			wantBody:     "Y is Z",
		},
		{
			name: "wrong_event_type",
			payload: []byte(`{
				"type": "event_callback",
				"event": {"type": "reaction_added", "channel": "DDMCHANNEL"}
			}`),
			wantErr: service.ErrUnrecognizedMessage,
		},
		{
			name:    "not_event_callback",
			payload: []byte(`{"type": "url_verification", "challenge": "xyz"}`),
			wantErr: service.ErrUnrecognizedMessage,
		},
		{
			name: "bot_message_skipped",
			payload: []byte(`{
				"type": "event_callback",
				"event": {
					"type": "message",
					"channel": "DDMCHANNEL",
					"user": "UBOT",
					"bot_id": "BBOT123",
					"text": "I am a bot",
					"ts": "1111111111.000003",
					"thread_ts": "1111111111.000001"
				}
			}`),
			wantErr: service.ErrUnrecognizedMessage,
		},
		{
			name: "no_thread_ts_skipped",
			payload: []byte(`{
				"type": "event_callback",
				"event": {
					"type": "message",
					"channel": "DDMCHANNEL",
					"user": "USPECIALIST",
					"text": "standalone DM",
					"ts": "1111111111.000004"
				}
			}`),
			wantErr: service.ErrUnrecognizedMessage,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := newProvider().ParseInbound(context.Background(), tc.payload)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error: got %v, want %v", err, tc.wantErr)
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

// TestParseInbound_PoolDMCorrelatesViaLedger verifies a reply on a pool
// candidate DM — whose (channel, thread_ts) is absent from the direct
// conversations-thread lookup (the pool path never writes it) — still
// correlates via the provider_messages ledger.
func TestParseInbound_PoolDMCorrelatesViaLedger(t *testing.T) {
	t.Parallel()

	convID := uuid.New()
	candidateID := uuid.New()

	members := newFakeMemberStore()
	members.add(model.Member{ID: candidateID, SlackUserID: "UCANDIDATE"})

	// Deliberately NOT registered in convs (the pool path never writes
	// conversations.slack_thread_ts) — only in the ledger.
	convs := newFakeConvStore()

	ledger := newFakeLedger()
	ledger.add("DPOOLCHANNEL", "2222222222.000001", model.ProviderMessage{
		ConversationID: convID,
		MemberID:       candidateID,
	})

	provider := slack.New(slack.Config{BotToken: "xoxb-test"}, members, convs, ledger)

	payload := []byte(`{
		"type": "event_callback",
		"event": {
			"type": "message",
			"channel": "DPOOLCHANNEL",
			"user": "UCANDIDATE",
			"text": "I'll take this one",
			"ts": "2222222222.000002",
			"thread_ts": "2222222222.000001"
		}
	}`)

	got, err := provider.ParseInbound(context.Background(), payload)
	if err != nil {
		t.Fatalf("ParseInbound: %v", err)
	}

	if got.ConversationID != convID {
		t.Errorf("ConversationID: got %v, want %v", got.ConversationID, convID)
	}

	if got.AuthorMemberID != candidateID {
		t.Errorf("AuthorMemberID: got %v, want %v", got.AuthorMemberID, candidateID)
	}

	if got.Body != "I'll take this one" {
		t.Errorf("Body: got %q", got.Body)
	}
}

// TestParseInbound_UnknownRefIsUnrecognized verifies neither the
// conversations-thread lookup nor the ledger resolving a (channel, thread_ts)
// pair returns ErrUnrecognizedMessage, not an internal error.
func TestParseInbound_UnknownRefIsUnrecognized(t *testing.T) {
	t.Parallel()

	provider := slack.New(slack.Config{BotToken: "xoxb-test"}, newFakeMemberStore(), newFakeConvStore(), newFakeLedger())

	payload := []byte(`{
		"type": "event_callback",
		"event": {
			"type": "message",
			"channel": "DUNKNOWN",
			"user": "USOMEONE",
			"text": "orphan reply",
			"ts": "9999999999.000002",
			"thread_ts": "9999999999.000001"
		}
	}`)

	_, err := provider.ParseInbound(context.Background(), payload)
	if !errors.Is(err, service.ErrUnrecognizedMessage) {
		t.Fatalf("got %v, want ErrUnrecognizedMessage", err)
	}
}
