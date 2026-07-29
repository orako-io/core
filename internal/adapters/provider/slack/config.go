// SPDX-License-Identifier: AGPL-3.0-or-later

// Package slack is the real Slack v1 messaging provider adapter.
//
// It implements the domain service.Provider port using the Slack Web API
// (net/http). OAuth and live signing-secret verification are deferred to M8;
// the config seam is present so the wiring compiles today.
package slack

import (
	"net/http"
	"time"
)

const (
	defaultBaseURL     = "https://slack.com/api"
	defaultHTTPTimeout = 15 * time.Second
)

// Config holds the Slack-specific provider credentials.
// All secrets come from the operator's environment; nothing is hard-coded.
type Config struct {
	// BotToken is the Slack Bot User OAuth token (xoxb-...).
	// Required.
	BotToken string

	// SigningSecret is the Slack app's signing secret used to verify the
	// X-Slack-Signature on inbound webhook requests.
	// The Verifier in internal/infra/api/slack uses this field; it is also stored
	// here so the adapter carries all its Slack-specific config in one struct.
	SigningSecret string

	// BaseURL is the Slack API base URL. Leave empty for production
	// (https://slack.com/api). Override in tests to point at an httptest server.
	BaseURL string `exhaustruct:"optional"`

	// DashboardBaseURL is the Orako server's externally reachable base URL
	// (ORAKO_BASE_URL, e.g. https://app.orako.io), used to build the
	// conversation deep link appended when a message is truncated. Empty falls
	// back to a plain "in the dashboard" pointer.
	DashboardBaseURL string `exhaustruct:"optional"`

	// HTTPClient is the HTTP client used for Slack API calls.
	// nil defaults to a client with a bounded request timeout.
	HTTPClient *http.Client `exhaustruct:"optional"`
}

// apiBase returns the resolved base URL for Slack API calls.
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

	return newHTTPClient()
}

func newHTTPClient() *http.Client {
	return &http.Client{Timeout: defaultHTTPTimeout}
}
