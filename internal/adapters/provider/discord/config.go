// SPDX-License-Identifier: AGPL-3.0-or-later

// Package discord is the real Discord messaging provider adapter.
//
// Outbound delivery (provider.go) is a REST-only concern: creating the DM
// channel and sending/editing/posting messages via discordgo's REST client.
// Inbound (gateway.go) requires a persistent gateway websocket connection —
// Discord has no inbound webhook for bot DMs — which
// internal/infra/api/gatewaymgr supervises independently of this package's
// Provider type.
package discord

import "github.com/bwmarrin/discordgo"

// Config holds the Discord provider credentials.
type Config struct {
	// BotToken is the Discord bot token (without the "Bot " prefix;
	// discordgo.New adds it).
	BotToken string

	// DashboardBaseURL is the Orako server's externally reachable base URL
	// (ORAKO_BASE_URL, e.g. https://app.orako.io), used to build the
	// conversation deep link in the answer footer. Empty (base URL
	// unconfigured) omits the link.
	DashboardBaseURL string `exhaustruct:"optional"`

	// restEndpoint overrides discordgo's global API endpoint for tests, so a
	// Provider can be pointed at an httptest.Server instead of discord.com.
	// Never set from project credentials.
	restEndpoint string `exhaustruct:"optional"`
}

// NewForTest builds a Provider whose REST calls are redirected to restEndpoint
// instead of the real Discord API. Test-only seam: production wiring always
// goes through New via the registry.
func NewForTest(cfg Config, restEndpoint string, members memberLookup, webhooks webhookCache) *Provider {
	cfg.restEndpoint = restEndpoint
	return New(cfg, members, webhooks)
}

// newSession builds a discordgo.Session for REST use (no gateway connection).
// When cfg.restEndpoint is set, the session's endpoints are rewritten to
// point at it — the test-only seam for exercising Deliver/Edit/PostChannel
// against a fake server instead of the real Discord API.
func newSession(cfg Config) (*discordgo.Session, error) {
	s, err := discordgo.New("Bot " + cfg.BotToken)
	if err != nil {
		return nil, err
	}

	if cfg.restEndpoint != "" {
		s.Client.Transport = &endpointRedirectTransport{target: cfg.restEndpoint}
	}

	return s, nil
}
