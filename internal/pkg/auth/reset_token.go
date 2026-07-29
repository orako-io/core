// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// resetAudience isolates password-reset tokens from session and invite tokens: a
// reset token can only be spent on the reset endpoint, never replayed as a
// session token or an invite acceptance.
const resetAudience = "orako-reset"

// MintResetToken signs a short-lived, single-use token proving control of an
// email. The "rv" claim binds it to the account's current password_reset_version,
// so the token is invalidated once used (the version is bumped) or after any other
// password change (L1).
func MintResetToken(secret, email string, resetVersion int, ttl time.Duration, now time.Time) (string, error) {
	claims := jwt.MapClaims{
		"sub": email,
		"aud": resetAudience,
		"rv":  resetVersion,
		"iat": now.Unix(),
		"exp": now.Add(ttl).Unix(),
	}

	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// VerifyResetToken checks the signature, audience, and expiry and returns the
// email plus the reset version it was minted against. Any failure is a single
// error so callers reject uniformly.
func VerifyResetToken(secret, token string) (email string, resetVersion int, err error) {
	claims := jwt.MapClaims{}

	_, err = jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return []byte(secret), nil
	}, jwt.WithAudience(resetAudience), jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return "", 0, err
	}

	email, _ = claims["sub"].(string)
	if email == "" {
		return "", 0, errors.New("auth: reset token has no email")
	}

	rv, _ := claims["rv"].(float64) // JSON numbers decode as float64

	return email, int(rv), nil
}
