// SPDX-License-Identifier: AGPL-3.0-or-later

package slack

import (
	"testing"

	"github.com/google/uuid"
)

func TestOAuthStateBindsAuthorizedTenantScope(t *testing.T) {
	t.Parallel()

	handler := NewOAuthHandler(OAuthConfig{
		ClientID:             "client",
		ClientSecret:         "secret",
		SigningSecret:        "signing-secret",
		BaseURL:              "https://orako.example.com",
		SlackBaseURL:         "",
		HTTPClient:           nil,
		InstallAuthenticator: nil,
		InstallAuthorizer:    nil,
	}, nil)
	want := InstallAuthorization{
		MemberID:  uuid.New(),
		OrgID:     uuid.New(),
		ProjectID: uuid.New(),
	}

	state, err := handler.generateState(want)
	if err != nil {
		t.Fatalf("generateState: %v", err)
	}

	got, ok := handler.consumeState(state)
	if !ok {
		t.Fatal("consumeState rejected a fresh state")
	}

	if got != want {
		t.Fatalf("state binding = %+v, want %+v", got, want)
	}

	if _, ok := handler.consumeState(state); ok {
		t.Fatal("state must be single-use")
	}
}
