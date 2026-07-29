// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/google/uuid"
)

// stubCredsLoader returns fixed credentials JSON.
type stubCredsLoader struct{ raw []byte }

func (s stubCredsLoader) LoadProvider(context.Context, uuid.UUID, string) ([]byte, error) {
	return s.raw, nil
}

// TestBotClientID proves the manage page's re-invite link derivation: the
// Discord bot token's first dot-segment decodes to the PUBLIC application id;
// anything malformed, non-Discord, or non-numeric yields "" (the UI then
// hides the button) — and the token itself never leaves the server.
func TestBotClientID(t *testing.T) {
	t.Parallel()

	appID := "123456789012345678"
	token := base64.RawURLEncoding.EncodeToString([]byte(appID)) + ".GfR3x.secret-part"

	srv := zeroServer()
	srv.providerCredentials = stubCredsLoader{raw: []byte(`{"bot_token":"` + token + `"}`)}

	if got := srv.botClientID(t.Context(), uuid.New(), "discord"); got != appID {
		t.Errorf("botClientID = %q, want %q", got, appID)
	}

	if got := srv.botClientID(t.Context(), uuid.New(), "slack"); got != "" {
		t.Errorf("non-discord kind must yield empty, got %q", got)
	}

	srv.providerCredentials = stubCredsLoader{raw: []byte(`{"bot_token":"not-a-real-token"}`)}
	if got := srv.botClientID(t.Context(), uuid.New(), "discord"); got != "" {
		t.Errorf("malformed token must yield empty, got %q", got)
	}

	srv.providerCredentials = stubCredsLoader{raw: []byte(`{}`)}
	if got := srv.botClientID(t.Context(), uuid.New(), "discord"); got != "" {
		t.Errorf("missing token must yield empty, got %q", got)
	}
}
