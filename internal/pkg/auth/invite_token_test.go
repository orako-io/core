// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/orako-io/core/internal/pkg/auth"
)

func TestInviteTokenRoundTrip(t *testing.T) {
	t.Parallel()

	tok, err := auth.MintInviteToken("secret", "invitee@example.com", time.Hour, time.Now())
	if err != nil {
		t.Fatalf("MintInviteToken: %v", err)
	}

	email, err := auth.VerifyInviteToken("secret", tok)
	if err != nil {
		t.Fatalf("VerifyInviteToken: %v", err)
	}

	if email != "invitee@example.com" {
		t.Errorf("email = %q, want invitee@example.com", email)
	}
}

func TestInviteTokenIsolatedFromSessionTokens(t *testing.T) {
	t.Parallel()

	const secret = "test-hs256-secret-at-least-32-bytes!!"

	// Wrong secret / expired are rejected.
	if _, err := auth.VerifyInviteToken("other", mustInvite(t, secret, time.Hour)); err == nil {
		t.Error("wrong secret must be rejected")
	}

	if _, err := auth.VerifyInviteToken(secret, mustInvite(t, secret, -time.Minute)); err == nil {
		t.Error("expired invite must be rejected")
	}

	// A session token (dashboard audience) must NOT verify as an invite token.
	session, _ := auth.MintHS256(secret, "orako", "orako-dashboard", "sub", "e@x.com", time.Hour, time.Now())
	if _, err := auth.VerifyInviteToken(secret, session); err == nil {
		t.Error("a session token must not verify as an invite token")
	}

	// An invite token must NOT verify as a session token (audience mismatch).
	v, _ := auth.NewHS256Verifier(secret, "", "orako-dashboard")
	if _, err := v.Verify(context.Background(), mustInvite(t, secret, time.Hour)); err == nil {
		t.Error("an invite token must not verify as a session token")
	}
}

func mustInvite(t *testing.T, secret string, ttl time.Duration) string {
	t.Helper()

	tok, err := auth.MintInviteToken(secret, "e@x.com", ttl, time.Now())
	if err != nil {
		t.Fatalf("MintInviteToken: %v", err)
	}

	return tok
}
