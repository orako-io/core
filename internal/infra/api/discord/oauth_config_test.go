// SPDX-License-Identifier: AGPL-3.0-or-later

package discord

import (
	"net/http"
	"testing"
	"time"
)

func TestOAuthConfigHTTPClient_DefaultsToBoundedClient(t *testing.T) {
	t.Parallel()

	if got := (OAuthConfig{}).httpClient().Timeout; got != defaultHTTPTimeout {
		t.Fatalf("HTTP timeout = %s, want %s", got, defaultHTTPTimeout)
	}
}

func TestOAuthConfigHTTPClient_PreservesInjectedClient(t *testing.T) {
	t.Parallel()

	injected := &http.Client{Timeout: time.Second}
	if got := (OAuthConfig{HTTPClient: injected}).httpClient(); got != injected {
		t.Fatal("OAuthConfig replaced the injected HTTP client")
	}
}
