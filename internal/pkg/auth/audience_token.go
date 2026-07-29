// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// signAudienceToken mints a short-lived, single-purpose HS256 token binding an
// email to a specific audience. Invite and reset tokens are thin wrappers over
// this that pin their own audience, so one can never be replayed as the other or
// as a session token.
func signAudienceToken(secret, email, audience string, ttl time.Duration, now time.Time) (string, error) {
	claims := jwt.MapClaims{
		"sub": email,
		"aud": audience,
		"iat": now.Unix(),
		"exp": now.Add(ttl).Unix(),
	}

	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// parseAudienceToken verifies the signature, audience, and expiry and returns the
// email. Any failure is a single error so callers reject uniformly.
func parseAudienceToken(secret, token, audience string) (string, error) {
	claims := jwt.MapClaims{}

	_, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return []byte(secret), nil
	}, jwt.WithAudience(audience), jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return "", err
	}

	email, _ := claims["sub"].(string)
	if email == "" {
		return "", errors.New("auth: token has no subject email")
	}

	return email, nil
}
