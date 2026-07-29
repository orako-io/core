// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"strings"
	"testing"
)

func TestValidateRegistrationRejectsUnboundedOrUnsupportedMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  registerRequest
	}{
		{
			name: "oversized client name",
			req: registerRequest{
				ClientName:   strings.Repeat("x", maxClientNameBytes+1),
				RedirectURIs: []string{"https://agent.example/callback"},
			},
		},
		{
			name: "duplicate redirect",
			req: registerRequest{
				RedirectURIs: []string{
					"https://agent.example/callback",
					"https://agent.example/callback",
				},
			},
		},
		{
			name: "unsupported grant",
			req: registerRequest{
				RedirectURIs: []string{"https://agent.example/callback"},
				GrantTypes:   []string{"client_credentials"},
			},
		},
		{
			name: "confidential client authentication",
			req: registerRequest{
				RedirectURIs: []string{"https://agent.example/callback"},
				AuthMethod:   "client_secret_basic",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if err := validateRegistration(&test.req); err == nil {
				t.Fatal("validateRegistration() error = nil, want rejection")
			}
		})
	}
}
