// SPDX-License-Identifier: AGPL-3.0-or-later

package teams

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// defaultServiceURL is the Bot Framework connector base URL used to reach a
// Teams conversation when the bot has not yet learned a tenant-specific one
// from an inbound activity. Bot Framework activities carry their own
// serviceUrl; a future enhancement can cache the one observed on inbound
// traffic and prefer it over this default.
const defaultServiceURL = "https://smba.trafficmanager.net/teams"

// defaultAADBase is the Azure AD v2 token endpoint host. Overridable in tests
// only (never via project credentials — the tenant path segment already
// scopes the request).
const defaultAADBase = "https://login.microsoftonline.com"

// defaultHTTPTimeout bounds every Teams HTTP call made with the default
// client (AAD token fetch, Bot Framework connector calls) so one stalled
// Microsoft endpoint cannot block Teams delivery indefinitely.
const defaultHTTPTimeout = 10 * time.Second

// defaultHTTPClient is the production default used when Config.HTTPClient is
// nil: a single shared, timeout-bound client (never the unbounded
// http.DefaultClient).
var defaultHTTPClient = &http.Client{Timeout: defaultHTTPTimeout} //nolint:gochecknoglobals // shared, timeout-bound default client; package-level by design

// Config holds the Microsoft Teams provider credentials.
// Fields correspond to the Azure App Registration and Bot Framework setup.
type Config struct {
	// TenantID is the Azure Active Directory tenant UUID.
	TenantID string
	// ClientID is the App Registration application (client) UUID.
	ClientID string
	// ClientSecret is the App Registration client secret.
	ClientSecret string
	// BotAppID is the Teams Bot application ID. It is both the "28:" prefix
	// used as the bot's participant id when creating a conversation and the
	// expected `aud` claim on inbound Bot Framework JWTs.
	BotAppID string

	// DashboardBaseURL is the Orako server's externally reachable base URL
	// (ORAKO_BASE_URL, e.g. https://app.orako.io), used to build the
	// conversation deep link appended when a message is truncated. Empty falls
	// back to a plain "in the dashboard" pointer.
	DashboardBaseURL string `exhaustruct:"optional"`

	// ServiceURL is the Bot Framework connector base URL for this project's
	// bot. Empty defaults to defaultServiceURL. Overridable via the
	// project's credentials map key "service_url" (optional) — validated at
	// ConfigureProvider time (command.validateTeamsServiceURL) and
	// re-validated here as a backstop; see serviceURL().
	ServiceURL string `exhaustruct:"optional"`

	// ServiceURLOverrideForTest bypasses the ServiceURL allowlist entirely
	// when non-empty. Test-only seam; never set from project credentials —
	// ConfigureProvider only ever populates ServiceURL, which IS validated.
	ServiceURLOverrideForTest string `exhaustruct:"optional"`

	// AADBaseURL overrides the AAD token endpoint host. Empty defaults to
	// defaultAADBase. Test-only seam; never set from project credentials.
	AADBaseURL string `exhaustruct:"optional"`

	// HTTPClient is the HTTP client used for AAD token and Bot Framework
	// connector calls. nil defaults to defaultHTTPClient (a shared client
	// bounded by defaultHTTPTimeout, not the unbounded http.DefaultClient).
	HTTPClient *http.Client `exhaustruct:"optional"`
}

// allowedServiceURLHostSuffixes are the only Bot Framework connector host
// suffixes this adapter will ever dial for the optional, tenant-supplied
// ServiceURL. Mirrors command.validateTeamsServiceURL, which rejects
// anything else at ConfigureProvider time; this is the adapter-side
// backstop so a value that somehow slipped through validation (e.g. a row
// persisted before that check existed) still can never make Deliver/Edit
// dial an arbitrary host (SSRF).
var allowedServiceURLHostSuffixes = []string{".botframework.com"} //nolint:gochecknoglobals // compile-time allowlist; package-level by design

// allowedServiceURLExactHosts pins connector hosts on shared namespaces:
// trafficmanager.net is an Azure-shared zone, so only the exact Bot Framework
// connector host is allowed, never any *.trafficmanager.net.
var allowedServiceURLExactHosts = []string{"smba.trafficmanager.net"} //nolint:gochecknoglobals // compile-time allowlist; package-level by design

// serviceURL returns the resolved Bot Framework connector base URL: the
// test-only override when set, else the configured ServiceURL when it
// validates (https + allowlisted host suffix), else defaultServiceURL. Never
// returns an unvalidated, tenant-supplied host.
func (c Config) serviceURL() string {
	if c.ServiceURLOverrideForTest != "" {
		return c.ServiceURLOverrideForTest
	}

	if c.ServiceURL != "" && validServiceURL(c.ServiceURL) {
		return c.ServiceURL
	}

	return defaultServiceURL
}

// validServiceURL reports whether raw is an https URL whose host ends in an
// allowlisted Bot Framework connector suffix.
func validServiceURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return false
	}

	host := parsed.Hostname()

	if slices.Contains(allowedServiceURLExactHosts, host) {
		return true
	}

	for _, suffix := range allowedServiceURLHostSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}

	return false
}

// aadBase returns the resolved AAD token endpoint host.
func (c Config) aadBase() string {
	if c.AADBaseURL != "" {
		return c.AADBaseURL
	}

	return defaultAADBase
}

// httpClient returns the resolved HTTP client.
func (c Config) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}

	return defaultHTTPClient
}
