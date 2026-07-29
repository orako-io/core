// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTrustedProxyRealIPIgnoresUntrustedForwardingHeader(t *testing.T) {
	t.Parallel()

	got := ""
	handler := trustedProxyRealIP("10.0.0.0/8", slog.New(slog.DiscardHandler))(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.RemoteAddr }),
	)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	request.RemoteAddr = "203.0.113.10:1234"
	request.Header.Set("X-Forwarded-For", "198.51.100.20")

	handler.ServeHTTP(httptest.NewRecorder(), request)

	if got != "203.0.113.10:1234" {
		t.Fatalf("RemoteAddr = %q, want untrusted peer unchanged", got)
	}
}

func TestTrustedProxyRealIPUsesFirstUntrustedHop(t *testing.T) {
	t.Parallel()

	got := ""
	handler := trustedProxyRealIP("10.0.0.0/8", slog.New(slog.DiscardHandler))(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.RemoteAddr }),
	)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.2:1234"
	request.Header.Set("X-Forwarded-For", "198.51.100.20, 10.0.0.3")

	handler.ServeHTTP(httptest.NewRecorder(), request)

	if got != "198.51.100.20:0" {
		t.Fatalf("RemoteAddr = %q, want forwarded client", got)
	}
}
