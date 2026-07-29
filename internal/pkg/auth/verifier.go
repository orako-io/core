// SPDX-License-Identifier: Apache-2.0

// Package auth verifies the JWTs Orako accepts as proof of *identity* —
// signature and standard claims only. It says nothing about authorization: the
// caller's role always comes from Orako's RBAC tables, never the token.
package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
)

// Claims is the verified, transport-agnostic identity extracted from a token.
type Claims struct {
	// Subject is the issuer-scoped stable user id (the JWT "sub").
	Subject string
	// Email is the user's email when the token carries one; may be empty.
	Email string `exhaustruct:"optional"`
}

// TokenVerifier validates a raw JWT (without the "Bearer " prefix) and returns
// its identity claims, or an error when the token is invalid or expired.
type TokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (Claims, error)
}

// HS256Verifier validates symmetric HS256 JWTs with a shared secret.
type HS256Verifier struct {
	secret   []byte
	issuer   string
	audience string
}

// minHS256SecretBytes is the shortest accepted shared secret. HS256 session,
// invite, and reset tokens all rest on this secret; a short/low-entropy value is
// brute-forceable offline from a captured token, so fail closed below 256 bits.
const minHS256SecretBytes = 32

// NewHS256Verifier builds an HS256 verifier. secret must be at least 32 bytes;
// issuer and audience are enforced only when non-empty.
func NewHS256Verifier(secret, issuer, audience string) (*HS256Verifier, error) {
	if secret == "" {
		return nil, errors.New("auth: HS256 secret is required")
	}

	if len(secret) < minHS256SecretBytes {
		return nil, fmt.Errorf("auth: HS256 secret must be at least %d bytes (got %d)", minHS256SecretBytes, len(secret))
	}

	return &HS256Verifier{secret: []byte(secret), issuer: issuer, audience: audience}, nil
}

// Verify validates the token's HS256 signature and standard claims.
func (v *HS256Verifier) Verify(_ context.Context, rawToken string) (Claims, error) {
	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithExpirationRequired(),
	}
	if v.issuer != "" {
		opts = append(opts, jwt.WithIssuer(v.issuer))
	}

	if v.audience != "" {
		opts = append(opts, jwt.WithAudience(v.audience))
	}

	claims := jwt.MapClaims{}

	_, err := jwt.ParseWithClaims(rawToken, claims, func(*jwt.Token) (any, error) {
		return v.secret, nil
	}, opts...)
	if err != nil {
		return Claims{}, fmt.Errorf("auth: invalid HS256 token: %w", err)
	}

	sub, _ := claims["sub"].(string)
	if sub == "" {
		return Claims{}, errors.New("auth: token has no subject (sub) claim")
	}

	email, _ := claims["email"].(string)

	return Claims{Subject: sub, Email: email}, nil
}

// OIDCVerifier validates asymmetric JWTs against an OIDC issuer's JWKS. Key
// discovery, caching and rotation are handled by go-oidc.
type OIDCVerifier struct {
	verifier *oidc.IDTokenVerifier
}

// NewOIDCVerifier discovers the issuer's OIDC metadata and builds a verifier.
// When audience is empty the audience check is skipped. The provided context is
// used only for discovery; it need not outlive this call.
func NewOIDCVerifier(ctx context.Context, issuer, audience string) (*OIDCVerifier, error) {
	if issuer == "" {
		return nil, errors.New("auth: OIDC issuer is required")
	}

	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("auth: OIDC discovery for %q: %w", issuer, err)
	}

	cfg := &oidc.Config{ClientID: audience}
	if audience == "" {
		cfg.SkipClientIDCheck = true
	}

	return &OIDCVerifier{verifier: provider.Verifier(cfg)}, nil
}

// Verify validates the token's signature (via JWKS) and standard claims.
func (v *OIDCVerifier) Verify(ctx context.Context, rawToken string) (Claims, error) {
	idToken, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return Claims{}, fmt.Errorf("auth: invalid OIDC token: %w", err)
	}

	var extra struct {
		Email string `json:"email"`
	}

	// Email is optional; ignore a decode miss.
	_ = idToken.Claims(&extra)

	if idToken.Subject == "" {
		return Claims{}, errors.New("auth: token has no subject (sub) claim")
	}

	return Claims{Subject: idToken.Subject, Email: extra.Email}, nil
}
