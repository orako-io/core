// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/orako-io/core/internal/pkg/auth"
)

const testSecret = "super-secret-signing-key-32bytes!"

func mintHS256(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	s, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}

	return s
}

func TestHS256Verifier_ValidToken(t *testing.T) {
	t.Parallel()

	v, err := auth.NewHS256Verifier(testSecret, "", "")
	if err != nil {
		t.Fatalf("NewHS256Verifier: %v", err)
	}

	raw := mintHS256(t, testSecret, jwt.MapClaims{
		"sub":   "user-123",
		"email": "sarah@example.com",
		"exp":   time.Now().Add(time.Hour).Unix(),
	})

	claims, err := v.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if claims.Subject != "user-123" || claims.Email != "sarah@example.com" {
		t.Errorf("claims = %+v", claims)
	}
}

func TestHS256Verifier_Rejects(t *testing.T) {
	t.Parallel()

	valid := jwt.MapClaims{"sub": "u", "email": "e@x.com", "exp": time.Now().Add(time.Hour).Unix()}

	cases := []struct {
		name   string
		verify func() (*auth.HS256Verifier, error)
		token  string
	}{
		{
			name:   "expired",
			verify: func() (*auth.HS256Verifier, error) { return auth.NewHS256Verifier(testSecret, "", "") },
			token:  mintHS256(t, testSecret, jwt.MapClaims{"sub": "u", "exp": time.Now().Add(-time.Hour).Unix()}),
		},
		{
			name:   "wrong secret",
			verify: func() (*auth.HS256Verifier, error) { return auth.NewHS256Verifier(testSecret, "", "") },
			token:  mintHS256(t, "a-different-secret-value-of-length!", valid),
		},
		{
			name:   "missing sub",
			verify: func() (*auth.HS256Verifier, error) { return auth.NewHS256Verifier(testSecret, "", "") },
			token:  mintHS256(t, testSecret, jwt.MapClaims{"email": "e@x.com", "exp": time.Now().Add(time.Hour).Unix()}),
		},
		{
			name:   "wrong issuer",
			verify: func() (*auth.HS256Verifier, error) { return auth.NewHS256Verifier(testSecret, "https://want", "") },
			token:  mintHS256(t, testSecret, jwt.MapClaims{"sub": "u", "iss": "https://other", "exp": time.Now().Add(time.Hour).Unix()}),
		},
		{
			name:   "wrong audience",
			verify: func() (*auth.HS256Verifier, error) { return auth.NewHS256Verifier(testSecret, "", "orako") },
			token:  mintHS256(t, testSecret, jwt.MapClaims{"sub": "u", "aud": "other", "exp": time.Now().Add(time.Hour).Unix()}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v, err := tc.verify()
			if err != nil {
				t.Fatalf("verifier: %v", err)
			}

			if _, err := v.Verify(context.Background(), tc.token); err == nil {
				t.Fatalf("expected verification to fail for %s", tc.name)
			}
		})
	}
}

func TestNewHS256Verifier_RequiresSecret(t *testing.T) {
	t.Parallel()

	if _, err := auth.NewHS256Verifier("", "", ""); err == nil {
		t.Error("expected error for empty secret")
	}
}
