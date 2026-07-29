// SPDX-License-Identifier: AGPL-3.0-or-later

package teams

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/service"
)

// activity is the subset of the Bot Framework Activity schema Orako reads
// from an inbound webhook POST.
type activity struct {
	Type         string          `json:"type"`
	ID           string          `json:"id"`
	Text         string          `json:"text"`
	From         participant     `json:"from"`
	Conversation conversationRef `json:"conversation"`
	Value        json.RawMessage `json:"value"`
}

type participant struct {
	ID          string `json:"id"`
	AadObjectID string `json:"aadObjectId"`
}

type conversationRef struct {
	ID string `json:"id"`
}

// legacyClaimAction is the Action.Submit data payload value the retired Claim
// button carried; a click on a legacy card still delivered before the claim
// teardown is recognized and dropped (never appended as a reply).
const legacyClaimAction = "claim"

// claimActionValue is the retired Claim button's Action.Submit data payload,
// kept only so ParseInbound can recognize and drop a legacy click.
type claimActionValue struct {
	Action string `json:"action"`
}

// ParseInbound maps a raw Bot Framework Activity to a normalized
// InboundMessage: a plain-text "message" activity with no claim action.
//
// Correlation: from.aadObjectId → member (ByTeamsUserID); conversation.id →
// conversation via the delivery ledger's LatestByChannel — a Teams personal
// chat has no per-message reference the client reliably sets (every message
// in the 1:1 conversation shares one conversation id), unlike Slack's
// thread_ts, so exact (channel, ref) correlation does not apply here.
//
// Returns service.ErrUnrecognizedMessage when the payload is not a plain
// text message activity (including a legacy Claim-button click, dropped as a
// no-op) or correlation misses.
func (p *Provider) ParseInbound(ctx context.Context, raw []byte) (service.InboundMessage, error) {
	var act activity
	if err := json.Unmarshal(raw, &act); err != nil {
		return service.InboundMessage{}, fmt.Errorf("teams parse inbound: unmarshal: %w", err)
	}

	if act.Type != "message" || strings.TrimSpace(act.Text) == "" {
		return service.InboundMessage{}, service.ErrUnrecognizedMessage
	}

	if len(act.Value) > 0 {
		var val claimActionValue
		if err := json.Unmarshal(act.Value, &val); err == nil && val.Action == legacyClaimAction {
			// A click on a legacy Claim card, not a reply — drop it.
			return service.InboundMessage{}, service.ErrUnrecognizedMessage
		}
	}

	if act.Conversation.ID == "" || act.From.AadObjectID == "" {
		return service.InboundMessage{}, service.ErrUnrecognizedMessage
	}

	conversationID, err := p.resolveConversation(ctx, act.Conversation.ID)
	if err != nil {
		return service.InboundMessage{}, err
	}

	member, err := p.members.ByTeamsUserID(ctx, act.From.AadObjectID)
	if err != nil {
		return service.InboundMessage{}, fmt.Errorf("teams parse inbound: correlating member: %w", err)
	}

	return service.InboundMessage{
		ConversationID: conversationID,
		AuthorMemberID: member.ID,
		Body:           act.Text,
	}, nil
}

// resolveConversation correlates a Teams conversation id to an Orako
// conversation via the delivery ledger's latest-open-row-per-channel lookup.
func (p *Provider) resolveConversation(ctx context.Context, conversationID string) (uuid.UUID, error) {
	if p.ledger == nil {
		return uuid.Nil, service.ErrUnrecognizedMessage
	}

	msg, err := p.ledger.LatestByChannel(ctx, conversationID)
	if err != nil {
		return uuid.Nil, service.ErrUnrecognizedMessage
	}

	return msg.ConversationID, nil
}
