// SPDX-License-Identifier: AGPL-3.0-or-later

package discord

import (
	"net/http"
	"net/url"
)

// endpointRedirectTransport rewrites the scheme and host of every outgoing
// request to target, preserving path/query/method/body. discordgo builds its
// request URLs from package-level endpoint variables (discordgo.EndpointAPI
// et al.), which are process-global; rewriting per-request via a custom
// RoundTripper instead keeps the redirect scoped to one Session (and safe for
// parallel tests) rather than mutating shared package state.
type endpointRedirectTransport struct {
	target string
}

// RoundTrip implements http.RoundTripper.
func (t *endpointRedirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	targetURL, err := url.Parse(t.target)
	if err != nil {
		return nil, err
	}

	redirected := req.Clone(req.Context())
	redirected.URL.Scheme = targetURL.Scheme
	redirected.URL.Host = targetURL.Host
	redirected.Host = targetURL.Host

	return http.DefaultTransport.RoundTrip(redirected)
}
