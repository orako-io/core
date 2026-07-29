// SPDX-License-Identifier: AGPL-3.0-or-later

// login.go and its siblings (reset, invite, seed) are the self-host
// authentication use cases: email+password login, password reset, invite
// acceptance, and admin seeding. In "local" auth mode the server is its own
// identity provider (bcrypt hashes + a forged HS256 session JWT); the SaaS uses
// an external IdP (Supabase OIDC) instead and does not wire these.

package command

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/auth"
	"github.com/orako-io/core/internal/pkg/errs"
)

// ErrInvalidCredentials is returned for a bad email or password — the same error
// for both so a caller cannot probe which emails exist.
var ErrInvalidCredentials = errs.InvalidError{Field: "credentials", Reason: "invalid email or password"}

// credentialReader loads an account's login credential. Satisfied by
// *identity.AccountStore. ok is false for an unknown email OR an account with no
// local password (an IdP-only account).
type credentialReader interface {
	CredentialByEmail(ctx context.Context, email string) (accountID uuid.UUID, hash string, ok bool, err error)
}

// LocalSubject is the synthetic JWT subject for a local-auth account, derived
// deterministically from the account id. Registration stores the same value in
// accounts.subject so the principal resolver (which keys on subject) finds it.
func LocalSubject(accountID uuid.UUID) string {
	return "local:" + accountID.String()
}

// LoginHandler verifies an email+password and forges a short-lived HS256 session
// token.
type LoginHandler struct {
	creds    credentialReader
	secret   string
	issuer   string
	audience string
	ttl      time.Duration
	now      func() time.Time
}

// NewLoginHandler builds the handler. secret must be ORAKO_AUTH_HS256_SECRET;
// issuer/audience must match the local verifier's; ttl is the session lifetime.
func NewLoginHandler(creds credentialReader, secret, issuer, audience string, ttl time.Duration) LoginHandler {
	return LoginHandler{creds: creds, secret: secret, issuer: issuer, audience: audience, ttl: ttl, now: time.Now}
}

// Login returns a signed session token for a valid email+password, or
// ErrInvalidCredentials (uniform for unknown email and wrong password).
func (h LoginHandler) Login(ctx context.Context, email, password string) (string, error) {
	accountID, hash, ok, err := h.creds.CredentialByEmail(ctx, email)
	if err != nil {
		return "", errs.InternalError{Err: fmt.Errorf("loading credential: %w", err)}
	}

	// Always run a bcrypt comparison (against a dummy hash when the account is
	// absent) so response latency doesn't reveal whether the email has a local
	// account — closes the timing-enumeration side channel (M3).
	match := auth.VerifyPasswordConstantTime(hash, password)
	if !ok || !match {
		return "", ErrInvalidCredentials
	}

	token, err := auth.MintHS256(h.secret, h.issuer, h.audience, LocalSubject(accountID), email, h.ttl, h.now())
	if err != nil {
		return "", errs.InternalError{Err: fmt.Errorf("minting token: %w", err)}
	}

	return token, nil
}
