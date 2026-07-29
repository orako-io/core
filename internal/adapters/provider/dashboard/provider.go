// SPDX-License-Identifier: AGPL-3.0-or-later

// Package dashboard is the in-app delivery provider: it satisfies the domain
// service.Provider port for members whose delivery_channel is "dashboard".
//
// Unlike the external providers (Slack/Telegram/Teams), the dashboard channel
// makes no outbound API call. By the time Deliver is invoked the conversation
// and its initial question message have already been persisted by the command
// handler, and the in-app inbox is a read-model query over open conversations
// whose responder is on the dashboard channel. Delivery is therefore a
// successful no-op: there is nothing external to send and nothing extra to mark.
//
// Inbound (a responder's reply) does not arrive as a webhook payload to parse —
// it comes from the dashboard composer calling the FollowUp RPC, which already
// knows the conversation ID. ParseInbound is therefore never used for this
// channel and returns ErrUnrecognizedMessage.
//
// The dashboard channel is always available and needs no configuration, so it
// is the default channel and the fallback that guarantees Ask never
// orphans a conversation for lack of a configured provider.
package dashboard

import (
	"context"

	"github.com/orako-io/core/internal/application/service"
)

// Compile-time assertion: Provider must satisfy the domain Provider port.
var _ service.Provider = (*Provider)(nil)

// Provider is the in-app dashboard delivery adapter. It is stateless.
type Provider struct{}

// New builds a dashboard Provider. It takes no configuration: the dashboard
// channel is always available.
func New() *Provider {
	return &Provider{}
}

// Deliver is a successful no-op. The conversation and question message are
// persisted before Deliver is called; the in-app inbox surfaces them via a
// query, so there is no external call to make and no extra state to write.
// The returned MessageRef is the zero value: the dashboard has nothing to
// correlate inbound replies against (they arrive via the FollowUp RPC).
func (p *Provider) Deliver(_ context.Context, _ service.OutboundMessage) (service.MessageRef, error) {
	return service.MessageRef{ChannelID: "", MessageID: ""}, nil
}

// ParseInbound always returns ErrUnrecognizedMessage: dashboard replies arrive
// through the FollowUp RPC (the composer), not through a webhook payload.
func (p *Provider) ParseInbound(_ context.Context, _ []byte) (service.InboundMessage, error) {
	return service.InboundMessage{}, service.ErrUnrecognizedMessage
}
