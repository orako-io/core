// SPDX-License-Identifier: AGPL-3.0-or-later

package teams

import "testing"

// TestConfig_ServiceURL_ValidatesAllowlist proves serviceURL() only returns a
// configured ServiceURL when it validates (https scheme + allowlisted host
// suffix); anything else falls back to defaultServiceURL. This is the
// adapter-side SSRF backstop: even if an invalid value slipped past
// ConfigureProvider's own validation (command.validateTeamsServiceURL, a
// separate layer), the adapter must never dial an arbitrary,
// tenant-supplied host.
func TestConfig_ServiceURL_ValidatesAllowlist(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "empty_falls_back", value: "", want: defaultServiceURL},
		{name: "valid_trafficmanager", value: "https://smba.trafficmanager.net/emea/", want: "https://smba.trafficmanager.net/emea/"},
		{name: "valid_botframework", value: "https://smba.botframework.com/emea/", want: "https://smba.botframework.com/emea/"},
		// trafficmanager.net is a shared Azure namespace: any *.trafficmanager.net
		// other than the exact connector host must be rejected (SSRF / token exfil).
		{name: "attacker_trafficmanager_falls_back", value: "https://attacker.trafficmanager.net/x", want: defaultServiceURL},
		{name: "http_scheme_falls_back", value: "http://smba.trafficmanager.net/emea/", want: defaultServiceURL},
		{name: "internal_ip_falls_back", value: "https://10.0.0.5/x", want: defaultServiceURL},
		{name: "arbitrary_host_falls_back", value: "https://evil.example.com/x", want: defaultServiceURL},
		{name: "malformed_falls_back", value: "://bad", want: defaultServiceURL},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := Config{ServiceURL: tc.value}
			if got := cfg.serviceURL(); got != tc.want {
				t.Errorf("serviceURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestConfig_ServiceURL_TestOverrideBypassesAllowlist proves
// ServiceURLOverrideForTest — the seam adapter tests use to point at an
// httptest server — is honored regardless of ServiceURL's validity, so it
// never fights the allowlist added for the SSRF fix.
func TestConfig_ServiceURL_TestOverrideBypassesAllowlist(t *testing.T) {
	t.Parallel()

	cfg := Config{
		ServiceURL:                "https://evil.example.com/x", // would fail the allowlist alone
		ServiceURLOverrideForTest: "http://127.0.0.1:9999",
	}

	if got, want := cfg.serviceURL(), "http://127.0.0.1:9999"; got != want {
		t.Errorf("serviceURL() = %q, want %q", got, want)
	}
}
