// SPDX-License-Identifier: AGPL-3.0-or-later

package teams_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/provider/teams"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

func messageActivity(t *testing.T, text, conversationID, aadObjectID string) []byte {
	t.Helper()

	raw, err := json.Marshal(map[string]any{
		"type": "message",
		"id":   "activity-2",
		"text": text,
		"from": map[string]string{"id": "29:user", "aadObjectId": aadObjectID},
		"conversation": map[string]string{
			"id": conversationID,
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return raw
}

// legacyClaimActivity builds an activity shaped like a click on a retired
// Claim Adaptive Card: an Action.Submit whose Value carries {"action":"claim"}.
// The text field mirrors the card's messageBack text, so the payload passes
// the plain-text gate and exercises the legacy-claim guard specifically.
func legacyClaimActivity(t *testing.T, activityType, text, conversationID string) []byte {
	t.Helper()

	raw, err := json.Marshal(map[string]any{
		"type": activityType,
		"id":   "activity-3",
		"text": text,
		"from": map[string]string{"id": "29:user", "aadObjectId": "aad-obj-1"},
		"conversation": map[string]string{
			"id": conversationID,
		},
		"value": map[string]string{
			"action":         "claim",
			"conversationId": conversationID,
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return raw
}

// TestParseInbound_CorrelatesReply proves a plain text message activity
// correlates via ByTeamsUserID and the ledger's LatestByChannel.
func TestParseInbound_CorrelatesReply(t *testing.T) {
	t.Parallel()

	members := newFakeMemberStore()
	authorID := uuid.New()
	members.add(model.Member{ID: authorID, TeamsUserID: "aad-obj-1"})

	ledger := newFakeLedger()
	convID := uuid.New()
	ledger.add("conv-1", "activity-1", model.ProviderMessage{ConversationID: convID})

	p := teams.New(teams.Config{TenantID: "t", ClientID: "c", ClientSecret: "s", BotAppID: "b"}, members, ledger)

	raw := messageActivity(t, "here's my answer", "conv-1", "aad-obj-1")

	inbound, err := p.ParseInbound(t.Context(), raw)
	if err != nil {
		t.Fatalf("ParseInbound: %v", err)
	}

	if inbound.ConversationID != convID {
		t.Errorf("ConversationID: got %v, want %v", inbound.ConversationID, convID)
	}

	if inbound.AuthorMemberID != authorID {
		t.Errorf("AuthorMemberID: got %v, want %v", inbound.AuthorMemberID, authorID)
	}

	if inbound.Body != "here's my answer" {
		t.Errorf("Body: got %q, want %q", inbound.Body, "here's my answer")
	}
}

// TestParseInbound_UnrecognizedCases proves non-message activities, empty
// text, and an unresolved conversation all return ErrUnrecognizedMessage
// (routine, ack-and-drop cases) — a message from an unrecognized member is
// covered separately below, since (mirroring slack/inbound.go) that is a
// harder anomaly the caller surfaces rather than silently swallows.
func TestParseInbound_UnrecognizedCases(t *testing.T) {
	t.Parallel()

	members := newFakeMemberStore()
	members.add(model.Member{ID: uuid.New(), TeamsUserID: "aad-obj-1"})

	ledger := newFakeLedger()
	ledger.add("conv-1", "activity-1", model.ProviderMessage{ConversationID: uuid.New()})

	p := teams.New(teams.Config{TenantID: "t", ClientID: "c", ClientSecret: "s", BotAppID: "b"}, members, ledger)

	cases := []struct {
		name string
		raw  []byte
	}{
		{name: "invoke_activity", raw: legacyClaimActivity(t, "invoke", "", "conv-1")},
		{name: "empty_text", raw: messageActivity(t, "", "conv-1", "aad-obj-1")},
		{name: "unknown_conversation", raw: messageActivity(t, "hi", "conv-unknown", "aad-obj-1")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := p.ParseInbound(t.Context(), tc.raw)
			if !errors.Is(err, service.ErrUnrecognizedMessage) {
				t.Errorf("got %v, want ErrUnrecognizedMessage", err)
			}
		})
	}
}

// TestParseInbound_UnknownMemberIsAHardError proves a recognized conversation
// with an unrecognized author's member correlation surfaces as a real error
// (not ErrUnrecognizedMessage) — mirrors slack/inbound.go's ParseInbound.
func TestParseInbound_UnknownMemberIsAHardError(t *testing.T) {
	t.Parallel()

	ledger := newFakeLedger()
	ledger.add("conv-1", "activity-1", model.ProviderMessage{ConversationID: uuid.New()})

	p := teams.New(teams.Config{TenantID: "t", ClientID: "c", ClientSecret: "s", BotAppID: "b"}, newFakeMemberStore(), ledger)

	_, err := p.ParseInbound(t.Context(), messageActivity(t, "hi", "conv-1", "aad-obj-unknown"))
	if err == nil {
		t.Fatal("want error for unknown member, got nil")
	}

	if errors.Is(err, service.ErrUnrecognizedMessage) {
		t.Errorf("got ErrUnrecognizedMessage, want a correlation error (the conversation WAS recognized)")
	}
}

// TestParseInbound_LegacyClaimClickIsDropped proves a click on a legacy Claim
// Adaptive Card — a "message" activity whose Value carries {"action":"claim"},
// still arriving from cards delivered before the claim teardown — is
// recognized and dropped as ErrUnrecognizedMessage, never appended as a reply,
// even when everything else about it would correlate.
func TestParseInbound_LegacyClaimClickIsDropped(t *testing.T) {
	t.Parallel()

	members := newFakeMemberStore()
	members.add(model.Member{ID: uuid.New(), TeamsUserID: "aad-obj-1"})

	ledger := newFakeLedger()
	ledger.add("conv-1", "activity-1", model.ProviderMessage{ConversationID: uuid.New()})

	p := teams.New(teams.Config{TenantID: "t", ClientID: "c", ClientSecret: "s", BotAppID: "b"}, members, ledger)

	_, err := p.ParseInbound(t.Context(), legacyClaimActivity(t, "message", "Claim", "conv-1"))
	if !errors.Is(err, service.ErrUnrecognizedMessage) {
		t.Errorf("got %v, want ErrUnrecognizedMessage (legacy claim click must be dropped)", err)
	}
}
