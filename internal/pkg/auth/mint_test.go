// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/orako-io/core/internal/pkg/auth"
)

// TestMintHS256RoundTrip proves a minted token verifies through the same HS256
// verifier the server uses in local mode, and the subject/email survive.
func TestMintHS256RoundTrip(t *testing.T) {
	t.Parallel()

	const (
		secret = "test-hs256-secret-at-least-32-bytes!!"
		issuer = "orako"
		aud    = "orako-dashboard"
	)

	tok, err := auth.MintHS256(secret, issuer, aud, "local:acct-1", "sam@example.com", time.Hour, time.Now())
	if err != nil {
		t.Fatalf("MintHS256: %v", err)
	}

	v, err := auth.NewHS256Verifier(secret, issuer, aud)
	if err != nil {
		t.Fatalf("NewHS256Verifier: %v", err)
	}

	claims, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if claims.Subject != "local:acct-1" {
		t.Errorf("Subject = %q, want local:acct-1", claims.Subject)
	}

	if claims.Email != "sam@example.com" {
		t.Errorf("Email = %q, want sam@example.com", claims.Email)
	}
}

func TestMintHS256Rejected(t *testing.T) {
	t.Parallel()

	const secret = "test-hs256-secret-at-least-32-bytes!!"

	v, _ := auth.NewHS256Verifier(secret, "orako", "aud")

	// Expired.
	expired, _ := auth.MintHS256(secret, "orako", "aud", "x", "", -time.Minute, time.Now())
	if _, err := v.Verify(context.Background(), expired); err == nil {
		t.Error("expired token must be rejected")
	}

	// Wrong secret.
	other, _ := auth.MintHS256("different-secret", "orako", "aud", "x", "", time.Hour, time.Now())
	if _, err := v.Verify(context.Background(), other); err == nil {
		t.Error("token signed with another secret must be rejected")
	}

	// Wrong audience.
	badAud, _ := auth.MintHS256(secret, "orako", "someone-else", "x", "", time.Hour, time.Now())
	if _, err := v.Verify(context.Background(), badAud); err == nil {
		t.Error("token for another audience must be rejected")
	}
}
