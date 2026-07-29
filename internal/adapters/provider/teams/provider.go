// SPDX-License-Identifier: AGPL-3.0-or-later

// Package teams is the real Microsoft Teams messaging provider adapter.
//
// It implements the domain service.Provider port (plus the optional Editor
// and ChannelPoster capabilities) over the Bot Framework REST API: a
// proactive 1:1 conversation is created for the recipient's TeamsUserID
// binding, the message is posted as plain text, and later edits rewrite the
// same activity.
package teams

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

// teamsMaxMessageRunes bounds an outbound message body. Teams caps a message at
// roughly 28 KB including markup; 28000 characters is a safe rune budget under
// that, past which the body is SPLIT into sequential activities (humans never
// get truncated content — hub-and-spoke phase 3). At this cap a split is rare.
const teamsMaxMessageRunes = 28000

// activityTypeMessage is the Bot Framework Activity "type" value for a plain
// message (as opposed to "invoke", "conversationUpdate", etc.).
const activityTypeMessage = "message"

// memberLookup is the narrow member-side dependency the Teams adapter needs.
type memberLookup interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Member, error)
	ByTeamsUserID(ctx context.Context, teamsUserID string) (model.Member, error)
}

// ledgerReader resolves a delivered activity's conversation for ParseInbound's
// correlation (a Teams personal chat has no per-message reference, so that
// path resolves by channel alone).
type ledgerReader interface {
	LatestByChannel(ctx context.Context, channelID string) (model.ProviderMessage, error)
}

// Provider is the real Microsoft Teams messaging adapter.
type Provider struct {
	cfg     Config
	members memberLookup
	ledger  ledgerReader
	tokens  *tokenSource
	client  *http.Client
}

// New builds a Teams Provider from the given config. members is required;
// ledger may be nil for callers that never exercise inbound correlation
// (e.g. direct construction in isolated tests).
func New(cfg Config, members memberLookup, ledger ledgerReader) *Provider {
	return &Provider{
		cfg:     cfg,
		members: members,
		ledger:  ledger,
		tokens:  newTokenSource(cfg),
		client:  cfg.httpClient(),
	}
}

// conversationParameters is the body of POST {serviceUrl}/v3/conversations.
type conversationParameters struct {
	Bot         channelAccount   `json:"bot"`
	IsGroup     bool             `json:"isGroup"`
	Members     []channelAccount `json:"members"`
	ChannelData channelData      `json:"channelData"`
}

type channelAccount struct {
	ID          string `json:"id"`
	AadObjectID string `json:"aadObjectId,omitempty"`
}

type channelData struct {
	Tenant tenantRef `json:"tenant"`
}

type tenantRef struct {
	ID string `json:"id"`
}

// createConversationResponse is the relevant subset of the connector's
// response to POST /v3/conversations.
type createConversationResponse struct {
	ID string `json:"id"`
}

// outboundActivity is the body of POST/PUT .../activities[/{id}].
type outboundActivity struct {
	Type        string       `json:"type"`
	Text        string       `json:"text,omitempty"`
	Attachments []attachment `json:"attachments,omitempty"`
}

type attachment struct {
	ContentType string `json:"contentType"`
	Content     any    `json:"content"`
}

// postActivityResponse is the relevant subset of the connector's response to
// POST/PUT an activity.
type postActivityResponse struct {
	ID string `json:"id"`
}

// Deliver creates (or reuses) the proactive 1:1 conversation with the
// recipient's Teams binding and posts the message as plain text.
func (p *Provider) Deliver(ctx context.Context, msg service.OutboundMessage) (service.MessageRef, error) {
	recipient := msg.Recipient()

	member, err := p.members.ByID(ctx, recipient)
	if err != nil {
		return service.MessageRef{}, fmt.Errorf("teams deliver: resolving responder: %w", err)
	}

	if member.TeamsUserID == "" {
		return service.MessageRef{}, fmt.Errorf("teams deliver: responder %s has no Teams user ID bound", recipient)
	}

	conversationID, err := p.createConversation(ctx, member.TeamsUserID)
	if err != nil {
		return service.MessageRef{}, fmt.Errorf("teams deliver: creating conversation: %w", err)
	}

	convURL := service.ConversationURL(p.cfg.DashboardBaseURL, msg.ConversationID)
	chunks := service.SplitForDelivery(service.FormatOutbound(msg, convURL), teamsMaxMessageRunes)

	return service.SendSplit(ctx, chunks, func(ctx context.Context, chunk string) (service.MessageRef, error) {
		activityID, err := p.postActivity(ctx, conversationID, outboundActivity{Type: activityTypeMessage, Text: chunk, Attachments: nil})
		if err != nil {
			return service.MessageRef{}, fmt.Errorf("teams deliver: posting activity: %w", err)
		}

		return service.MessageRef{ChannelID: conversationID, MessageID: activityID}, nil
	})
}

// Edit rewrites a previously delivered activity via PUT. Attachments are
// always dropped: a legacy activity delivered before the claim teardown may
// still carry a Claim Adaptive Card; an edit retires it.
func (p *Provider) Edit(ctx context.Context, ref service.MessageRef, text string) error {
	activity := outboundActivity{Type: activityTypeMessage, Text: text, Attachments: nil}

	if _, err := p.updateActivity(ctx, ref.ChannelID, ref.MessageID, activity); err != nil {
		return fmt.Errorf("teams edit: %w", err)
	}

	return nil
}

// PostChannel posts text to an existing conversation id (e.g. the project's
// alert channel) rather than creating a new one.
func (p *Provider) PostChannel(ctx context.Context, conversationID, text string) (service.MessageRef, error) {
	activityID, err := p.postActivity(ctx, conversationID, outboundActivity{Type: activityTypeMessage, Text: text, Attachments: nil})
	if err != nil {
		return service.MessageRef{}, fmt.Errorf("teams post channel: %w", err)
	}

	return service.MessageRef{ChannelID: conversationID, MessageID: activityID}, nil
}

// createConversation creates (or, per Bot Framework's stable personal-chat
// behavior, reuses) the 1:1 conversation with the given Teams user.
func (p *Provider) createConversation(ctx context.Context, teamsUserID string) (string, error) {
	body := conversationParameters{
		Bot:     channelAccount{ID: "28:" + p.cfg.BotAppID, AadObjectID: ""},
		IsGroup: false,
		Members: []channelAccount{{ID: teamsUserID, AadObjectID: teamsUserID}},
		ChannelData: channelData{
			Tenant: tenantRef{ID: p.cfg.TenantID},
		},
	}

	var out createConversationResponse
	if err := p.call(ctx, http.MethodPost, p.cfg.serviceURL()+"/v3/conversations", body, &out); err != nil {
		return "", err
	}

	return out.ID, nil
}

// postActivity POSTs an activity to a conversation and returns its id.
func (p *Provider) postActivity(ctx context.Context, conversationID string, activity outboundActivity) (string, error) {
	var out postActivityResponse
	if err := p.call(ctx, http.MethodPost, p.cfg.serviceURL()+"/v3/conversations/"+conversationID+"/activities", activity, &out); err != nil {
		return "", err
	}

	return out.ID, nil
}

// updateActivity PUTs an activity in place.
func (p *Provider) updateActivity(ctx context.Context, conversationID, activityID string, activity outboundActivity) (string, error) {
	var out postActivityResponse

	endpoint := p.cfg.serviceURL() + "/v3/conversations/" + conversationID + "/activities/" + activityID
	if err := p.call(ctx, http.MethodPut, endpoint, activity, &out); err != nil {
		return "", err
	}

	return out.ID, nil
}

// call performs an authenticated JSON request against the Bot Framework
// connector API and decodes the response into out (which may be nil to
// discard the body).
func (p *Provider) call(ctx context.Context, method, endpoint string, body, out any) error {
	token, err := p.tokens.Token(ctx)
	if err != nil {
		return fmt.Errorf("obtaining connector token: %w", err)
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body close errors are informational in defer

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("bot framework connector: unexpected status %d", resp.StatusCode)
	}

	if out == nil {
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	return nil
}
