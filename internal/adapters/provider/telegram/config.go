// SPDX-License-Identifier: AGPL-3.0-or-later

// Package telegram is the real Telegram Bot API messaging provider adapter.
//
// It implements the domain service.Provider port using the Telegram Bot
// API (net/http). Deliver posts a message to the responder's Telegram chat
// via sendMessage and records the returned message_id for inbound correlation.
// ParseInbound maps a Telegram Update webhook payload to an InboundMessage
// using reply_to_message correlation.
//
// Onboarding note: a responder's telegram_chat_id must be bound manually by
// an admin via AddMember (telegram_chat_id field). The chat ID is obtained
// when the responder first sends any message to the bot — Telegram includes
// from.id in the Update payload, which the admin reads from bot logs or a
// temporary debug webhook.
package telegram

import (
	"net/http"
	"time"
)

const (
	defaultBaseURL     = "https://api.telegram.org"
	defaultHTTPTimeout = 15 * time.Second
)

// Config holds the Telegram Bot API credentials.
type Config struct {
	// BotToken is the Telegram Bot API token issued by @BotFather (e.g. "123:ABC…").
	// Required.
	BotToken string

	// BaseURL is the Telegram API base URL. Leave empty for production
	// (https://api.telegram.org). Override in tests to point at an httptest server.
	BaseURL string `exhaustruct:"optional"`

	// DashboardBaseURL is the Orako server's externally reachable base URL
	// (ORAKO_BASE_URL, e.g. https://app.orako.io), used to build the
	// conversation deep link in the answer footer. Empty falls
	// back to a plain "in the dashboard" pointer.
	DashboardBaseURL string `exhaustruct:"optional"`

	// HTTPClient is the HTTP client used for Telegram API calls.
	// nil defaults to a client with a bounded request timeout.
	HTTPClient *http.Client `exhaustruct:"optional"`
}

// apiBase returns the resolved base URL for Telegram API calls.
func (c Config) apiBase() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}

	return defaultBaseURL
}

// httpClient returns the resolved HTTP client.
func (c Config) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}

	return &http.Client{Timeout: defaultHTTPTimeout}
}
