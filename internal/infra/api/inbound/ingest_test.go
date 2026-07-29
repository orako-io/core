// SPDX-License-Identifier: AGPL-3.0-or-later

package inbound

import (
	"context"
	"log/slog"
	"net"
	"testing"
)

// TestIsBlockedIP covers the SSRF address classifier: private, loopback,
// link-local (incl. the 169.254.169.254 cloud-metadata endpoint), ULA,
// unspecified and multicast are refused; public unicast is allowed.
func TestIsBlockedIP(t *testing.T) {
	t.Parallel()

	blocked := []string{
		"127.0.0.1", "::1", // loopback
		"10.1.2.3", "192.168.1.1", "172.16.0.1", "fc00::1", // private / ULA
		"169.254.169.254", "fe80::1", // link-local (metadata)
		"0.0.0.0", "::", // unspecified
		"224.0.0.1", // multicast
	}
	for _, s := range blocked {
		if !isBlockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}

	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111"}
	for _, s := range allowed {
		if isBlockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

// TestGuardDialAddress covers the dialer Control hook that runs on the resolved
// IP just before connect.
func TestGuardDialAddress(t *testing.T) {
	t.Parallel()

	refused := []string{"169.254.169.254:80", "127.0.0.1:443", "10.0.0.5:443", "[::1]:443", "not-an-ip:443"}
	for _, addr := range refused {
		if err := guardDialAddress("tcp", addr, nil); err == nil {
			t.Errorf("guardDialAddress(%q) should refuse", addr)
		}
	}

	if err := guardDialAddress("tcp", "8.8.8.8:443", nil); err != nil {
		t.Errorf("guardDialAddress(public) should pass, got %v", err)
	}
}

// TestDownloadRejectsNonHTTPSURL proves the scheme/host gate refuses non-https
// and hostless URLs before any request is issued.
func TestDownloadRejectsNonHTTPSURL(t *testing.T) {
	t.Parallel()

	i := NewIngestor(nil, nil, 0, slog.New(slog.DiscardHandler))

	for _, u := range []string{
		"http://cdn.example.com/x.png",
		"file:///etc/passwd",
		"ftp://host/y",
		"https:///no-host",
		"://bad",
	} {
		if _, err := i.download(context.Background(), u); err == nil {
			t.Errorf("download(%q) should be rejected", u)
		}
	}
}
