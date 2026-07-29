// SPDX-License-Identifier: AGPL-3.0-or-later

package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/service"
)

// slackEventCallback is the outer envelope of a Slack Events API event_callback.
// Only the fields relevant to inbound reply correlation are decoded.
type slackEventCallback struct {
	Type  string            `json:"type"`
	Event slackMessageEvent `json:"event"`
}

// slackMessageEvent is the inner event payload for Slack "message" events.
type slackMessageEvent struct {
	Type     string `json:"type"`
	Channel  string `json:"channel"`
	User     string `json:"user"`
	Text     string `json:"text"`
	Ts       string `json:"ts"`
	ThreadTs string `json:"thread_ts"`
	// BotID is non-empty for messages posted by bots (including the Orako bot itself).
	// Bot messages must be skipped to avoid processing Orako's own outbound messages
	// as inbound replies.
	BotID string `json:"bot_id"`
}

// ParseInbound maps a raw Slack Events API payload to a normalized InboundMessage.
//
// Correlation logic:
//   - channel + thread_ts → conversation (ConversationBySlackThread), the
//     direct-ask path.
//   - on a miss, channel + thread_ts → conversation via the provider_messages
//     ledger (ByProviderRef), the pool-DM path — a candidate replies in the
//     thread of their own Deliver'd message, which the ledger recorded but
//     conversations.slack_thread_ts never was (only the direct path writes it).
//   - event.user (slack_user_id) → member (BySlackUserID)
//
// Returns service.ErrUnrecognizedMessage when:
//   - the payload is not a message event_callback
//   - the event is from a bot (bot_id is set)
//   - thread_ts is absent (top-level DM messages without a thread context;
//     Orako only correlates threaded replies in M4)
//   - neither the conversations-thread lookup nor the ledger resolve a match
func (p *Provider) ParseInbound(ctx context.Context, raw []byte) (service.InboundMessage, error) {
	var cb slackEventCallback
	if err := json.Unmarshal(raw, &cb); err != nil {
		return service.InboundMessage{}, fmt.Errorf("slack parse inbound: unmarshal: %w", err)
	}

	if cb.Type != "event_callback" || cb.Event.Type != "message" {
		return service.InboundMessage{}, service.ErrUnrecognizedMessage
	}

	// Skip messages from bots (including Orako's own outbound messages).
	if cb.Event.BotID != "" {
		return service.InboundMessage{}, service.ErrUnrecognizedMessage
	}

	// Require a thread_ts for correlation. A non-threaded DM message has no
	// thread_ts; Orako stores the outbound ts as the thread_ts binding, so
	// only threaded replies are processed in M4.
	if cb.Event.ThreadTs == "" {
		return service.InboundMessage{}, service.ErrUnrecognizedMessage
	}

	conversationID, err := p.resolveConversation(ctx, cb.Event.Channel, cb.Event.ThreadTs)
	if err != nil {
		return service.InboundMessage{}, err
	}

	// Correlate slack_user_id → member.
	member, err := p.members.BySlackUserID(ctx, cb.Event.User)
	if err != nil {
		return service.InboundMessage{}, fmt.Errorf("slack parse inbound: correlating member: %w", err)
	}

	return service.InboundMessage{
		ConversationID: conversationID,
		AuthorMemberID: member.ID,
		Body:           cb.Event.Text,
	}, nil
}

// resolveConversation correlates (channel, thread_ts) to a conversation:
// first via the direct 1:1 thread binding, falling back to the pool delivery
// ledger on a miss.
func (p *Provider) resolveConversation(ctx context.Context, channel, threadTS string) (uuid.UUID, error) {
	conv, err := p.convs.ConversationBySlackThread(ctx, channel, threadTS)
	if err == nil {
		return conv.ID, nil
	}

	if !errors.Is(err, adaptererr.ErrNotFound) {
		return uuid.Nil, fmt.Errorf("slack parse inbound: correlating conversation: %w", err)
	}

	if p.ledger == nil {
		return uuid.Nil, service.ErrUnrecognizedMessage
	}

	msg, ledgerErr := p.ledger.ByProviderRef(ctx, channel, threadTS)
	if ledgerErr != nil {
		return uuid.Nil, service.ErrUnrecognizedMessage
	}

	return msg.ConversationID, nil
}
