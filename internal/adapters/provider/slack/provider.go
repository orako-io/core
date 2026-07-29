// SPDX-License-Identifier: AGPL-3.0-or-later

package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

var (
	_ service.Provider      = (*Provider)(nil)
	_ service.Editor        = (*Provider)(nil)
	_ service.ChannelPoster = (*Provider)(nil)
)

// memberLookup is the narrow member-side dependency the Slack adapter needs.
type memberLookup interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Member, error)
	BySlackUserID(ctx context.Context, slackUserID string) (model.Member, error)
}

// conversationAccess is the narrow conversation-side dependency.
type conversationAccess interface {
	ConversationBySlackThread(ctx context.Context, channelID, threadTS string) (model.Conversation, error)
	SetSlackThread(ctx context.Context, id uuid.UUID, channelID, threadTS string) error
}

// ledgerReader is the narrow delivery-ledger dependency: it resolves a pool
// DM's conversation from its provider ref, used by ParseInbound to correlate
// a reply when the direct conversations-thread lookup misses.
type ledgerReader interface {
	ByProviderRef(ctx context.Context, channelID, messageRef string) (model.ProviderMessage, error)
}

// Provider is the real Slack messaging adapter. Outbound delivery records the
// returned channel/thread_ts on the conversation for the direct-ask path;
// inbound correlation maps (channel, thread_ts) → conversation and
// slack_user_id → member.
type Provider struct {
	cfg     Config
	members memberLookup
	convs   conversationAccess
	ledger  ledgerReader
}

// slackTextMaxRunes is Slack's chat.postMessage top-level text cap: 40000
// characters (msg_too_long beyond it). The tighter 3000-char section-block
// limit died with the Block Kit Claim button (hub-and-spoke phase 2) — Orako
// now posts plain text, so 40000 is the binding cap; a longer body is SPLIT
// into sequential messages, never truncated (phase 3).
const slackTextMaxRunes = 40000

// New builds a Slack Provider. members and convs are required; nil values
// panic at call-time. ledger may be nil for callers that never exercise the
// pool-DM Edit/inbound-fallback path (e.g. direct-ask-only tests).
func New(cfg Config, members memberLookup, convs conversationAccess, ledger ledgerReader) *Provider {
	return &Provider{cfg: cfg, members: members, convs: convs, ledger: ledger}
}

// postMessageRequest is the JSON body sent to chat.postMessage / chat.update.
type postMessageRequest struct {
	Channel string       `json:"channel"`
	Ts      string       `json:"ts,omitempty"`
	Text    string       `json:"text"`
	Blocks  []slackBlock `json:"blocks,omitempty"`
	// Username overrides the visible author for identity mirroring
	// (hub-and-spoke phase 5); requires the chat:write.customize scope.
	Username string `json:"username,omitempty"`
}

// postMessageResponse is the relevant subset of the Slack chat.postMessage /
// chat.update response.
type postMessageResponse struct {
	OK      bool   `json:"ok"`
	Channel string `json:"channel"`
	Ts      string `json:"ts"`
	Error   string `json:"error"`
}

// slackBlock is the subset of Slack's Block Kit JSON shape Orako emits: a
// "section" block carrying the message text.
type slackBlock struct {
	Type string     `json:"type"`
	Text *slackText `json:"text,omitempty"`
}

// slackText is a Block Kit text composition object.
type slackText struct {
	Type string `json:"type"` // "mrkdwn"
	Text string `json:"text"`
}

// Deliver sends the question via a Slack DM to the message's recipient —
// RecipientMemberID for a pool candidate, ResponderMemberID for the legacy
// direct-ask path — and returns the channel/ts the message was posted to.
//
// The direct-ask path additionally records the channel/ts on the conversation
// (SetSlackThread) so the existing 1:1 inbound correlation keeps working; a
// pool candidate DM (RecipientMemberID set) is tracked solely through the
// provider_messages ledger, written by the delivery notifier after Deliver
// returns.
func (p *Provider) Deliver(ctx context.Context, msg service.OutboundMessage) (service.MessageRef, error) {
	recipient := msg.Recipient()

	member, err := p.members.ByID(ctx, recipient)
	if err != nil {
		return service.MessageRef{}, fmt.Errorf("slack deliver: resolving responder: %w", err)
	}

	if member.SlackUserID == "" {
		return service.MessageRef{}, fmt.Errorf("slack deliver: responder %s has no Slack user ID bound", recipient)
	}

	// Identity mirroring (hub-and-spoke phase 5): a relayed message posts
	// under the origin member's name where the workspace allows
	// (chat:write.customize); any failure falls through to the plain path —
	// a mirror degrades to the bot identity, never to silence.
	if msg.Kind == service.MessageKindRelay && msg.MirrorAuthor != "" {
		if ref, err := p.deliverAsIdentity(ctx, member.SlackUserID, msg); err == nil {
			return ref, nil
		}
	}

	convURL := service.ConversationURL(p.cfg.DashboardBaseURL, msg.ConversationID)
	chunks := service.SplitForDelivery(
		slackKindPrefix(msg.Kind)+service.FormatOutbound(msg, convURL),
		slackTextMaxRunes,
	)

	ref, err := service.SendSplit(ctx, chunks, func(ctx context.Context, chunk string) (service.MessageRef, error) {
		resp, err := p.postMessage(ctx, member.SlackUserID, chunk, nil)
		if err != nil {
			return service.MessageRef{}, fmt.Errorf("slack deliver: posting message: %w", err)
		}

		return service.MessageRef{ChannelID: resp.Channel, MessageID: resp.Ts}, nil
	})
	if err != nil {
		return service.MessageRef{}, err
	}

	if msg.RecipientMemberID == uuid.Nil {
		if err := p.convs.SetSlackThread(ctx, msg.ConversationID, ref.ChannelID, ref.MessageID); err != nil {
			return service.MessageRef{}, fmt.Errorf("slack deliver: recording thread binding: %w", err)
		}
	}

	return ref, nil
}

// Edit rewrites a previously delivered message via chat.update. Blocks are
// always dropped: a legacy message delivered before the claim teardown may
// still carry a Claim actions block; an edit retires it.
func (p *Provider) Edit(ctx context.Context, ref service.MessageRef, text string) error {
	if _, err := p.updateMessage(ctx, ref.ChannelID, ref.MessageID, text, nil); err != nil {
		return fmt.Errorf("slack edit: %w", err)
	}

	return nil
}

// deliverAsIdentity posts a relayed message under the origin member's name
// (username override). The body drops the inline name — the identity carries
// it visually.
func (p *Provider) deliverAsIdentity(ctx context.Context, slackUserID string, msg service.OutboundMessage) (service.MessageRef, error) {
	chunks := service.SplitForDelivery("💬 "+msg.Question, slackTextMaxRunes)

	return service.SendSplit(ctx, chunks, func(ctx context.Context, chunk string) (service.MessageRef, error) {
		resp, err := p.call(ctx, "/chat.postMessage", postMessageRequest{
			Channel:  slackUserID,
			Ts:       "",
			Text:     chunk,
			Blocks:   nil,
			Username: msg.MirrorAuthor,
		})
		if err != nil {
			return service.MessageRef{}, fmt.Errorf("slack identity post: %w", err)
		}

		return service.MessageRef{ChannelID: resp.Channel, MessageID: resp.Ts}, nil
	})
}

// PostChannel posts text to a named Slack channel (e.g. the project's alert
// channel) rather than DMing a member.
func (p *Provider) PostChannel(ctx context.Context, channelID, text string) (service.MessageRef, error) {
	resp, err := p.postMessage(ctx, channelID, text, nil)
	if err != nil {
		return service.MessageRef{}, fmt.Errorf("slack post channel: %w", err)
	}

	return service.MessageRef{ChannelID: resp.Channel, MessageID: resp.Ts}, nil
}

// postMessage calls chat.postMessage and returns the channel/ts from the
// response. channel may be a user ID (DM) or channel ID.
func (p *Provider) postMessage(ctx context.Context, channel, text string, blocks []slackBlock) (postMessageResponse, error) {
	return p.call(ctx, "/chat.postMessage", postMessageRequest{Channel: channel, Ts: "", Text: text, Blocks: blocks, Username: ""})
}

// updateMessage calls chat.update to rewrite an existing message in place.
func (p *Provider) updateMessage(ctx context.Context, channel, ts, text string, blocks []slackBlock) (postMessageResponse, error) {
	return p.call(ctx, "/chat.update", postMessageRequest{Channel: channel, Ts: ts, Text: text, Blocks: blocks, Username: ""})
}

// call POSTs body (JSON-encoded) to the given Slack Web API path and decodes
// the standard ok/error response envelope.
func (p *Provider) call(ctx context.Context, path string, body postMessageRequest) (postMessageResponse, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return postMessageResponse{}, fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.apiBase()+path, bytes.NewReader(encoded))
	if err != nil {
		return postMessageResponse{}, fmt.Errorf("building request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.cfg.BotToken)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := p.cfg.httpClient().Do(req)
	if err != nil {
		return postMessageResponse{}, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body close errors are informational in defer

	var result postMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return postMessageResponse{}, fmt.Errorf("decoding response: %w", err)
	}

	if !result.OK {
		return postMessageResponse{}, fmt.Errorf("slack API error: %s", result.Error)
	}

	return result, nil
}

// slackKindPrefix prepends a short label to the DM text for a message kind
// distinct from the default question rendering — the review's "MessageKind
// is dead API" finding's minimal, real fix: the field must genuinely vary at
// least one adapter's behavior rather than being written everywhere and read
// nowhere. MessageKindQuestion (and the zero value) render with no prefix,
// unchanged from before this existed.
func slackKindPrefix(kind service.MessageKind) string {
	switch kind {
	case service.MessageKindNudge:
		return "🔔 Reminder: "
	case service.MessageKindClosure:
		return "📌 "
	case service.MessageKindQuestion, service.MessageKindHistory, service.MessageKindRelay:
		return "" // relay carries its own light "💬 " chrome from FormatOutbound.
	default:
		return "" // zero value (unset Kind): renders identically to a question.
	}
}
