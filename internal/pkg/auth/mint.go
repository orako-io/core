// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// MintHS256 signs a short-lived HS256 JWT that an HS256Verifier with the same
// secret/issuer/audience accepts. This is the "forge" the self-host local login
// uses to issue a session token after an email+password check — in local mode
// there is no external IdP to mint one. secret must match ORAKO_AUTH_HS256_SECRET.
func MintHS256(secret, issuer, audience, subject, email string, ttl time.Duration, now time.Time) (string, error) {
	claims := jwt.MapClaims{
		"sub": subject,
		"iat": now.Unix(),
		"exp": now.Add(ttl).Unix(),
	}

	if issuer != "" {
		claims["iss"] = issuer
	}

	if audience != "" {
		claims["aud"] = audience
	}

	if email != "" {
		claims["email"] = email
	}

	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}
