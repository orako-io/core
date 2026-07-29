// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import "testing"

// TestValidateRedirectURI proves DCR rejects cleartext non-loopback and
// fragment-bearing redirect URIs while accepting https and http loopback.
func TestValidateRedirectURI(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  string
		ok   bool
	}{
		{name: "https ok", uri: "https://client.example/cb", ok: true},
		{name: "http loopback ok", uri: "http://127.0.0.1:8976/cb", ok: true},
		{name: "http localhost ok", uri: "http://localhost:1234/cb", ok: true},
		{name: "http non-loopback rejected", uri: "http://attacker.example/cb", ok: false},
		{name: "fragment rejected", uri: "https://client.example/cb#x", ok: false},
		{name: "relative rejected", uri: "/cb", ok: false},
		{name: "custom scheme rejected", uri: "com.evil.app://cb", ok: false},
		{name: "garbage rejected", uri: "://nope", ok: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validateRedirectURI(tc.uri)
			if tc.ok && err != nil {
				t.Errorf("validateRedirectURI(%q) = %v, want nil", tc.uri, err)
			}

			if !tc.ok && err == nil {
				t.Errorf("validateRedirectURI(%q) = nil, want error", tc.uri)
			}
		})
	}
}
