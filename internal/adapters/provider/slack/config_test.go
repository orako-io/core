// SPDX-License-Identifier: AGPL-3.0-or-later

package slack

import (
	"net/http"
	"testing"
	"time"
)

func TestHTTPClient_DefaultsToBoundedClient(t *testing.T) {
	t.Parallel()

	if got := (Config{}).httpClient().Timeout; got != defaultHTTPTimeout {
		t.Fatalf("Config HTTP timeout = %s, want %s", got, defaultHTTPTimeout)
	}

	if got := NewAPIDirectory("token", "", nil).client.Timeout; got != defaultHTTPTimeout {
		t.Fatalf("APIDirectory HTTP timeout = %s, want %s", got, defaultHTTPTimeout)
	}

	if got := NewOrgDirectory(nil).client.Timeout; got != defaultHTTPTimeout {
		t.Fatalf("OrgDirectory HTTP timeout = %s, want %s", got, defaultHTTPTimeout)
	}
}

func TestHTTPClient_PreservesInjectedClient(t *testing.T) {
	t.Parallel()

	injected := &http.Client{Timeout: time.Second}

	if got := (Config{HTTPClient: injected}).httpClient(); got != injected {
		t.Fatal("Config replaced the injected HTTP client")
	}

	if got := NewAPIDirectory("token", "", injected).client; got != injected {
		t.Fatal("APIDirectory replaced the injected HTTP client")
	}
}
