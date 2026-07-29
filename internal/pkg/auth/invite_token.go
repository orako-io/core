// SPDX-License-Identifier: Apache-2.0

package auth

import "time"

// inviteAudience isolates invite tokens from session and reset tokens: a session
// verifier expects the dashboard audience, so an invite token can never be used
// as a session token, and vice versa.
const inviteAudience = "orako-invite"

// MintInviteToken signs a short-lived, single-purpose token that proves an email
// was invited. The self-host local invite-link generator embeds it in the invite
// email; AcceptInvite verifies it before creating the account.
func MintInviteToken(secret, email string, ttl time.Duration, now time.Time) (string, error) {
	return signAudienceToken(secret, email, inviteAudience, ttl, now)
}

// VerifyInviteToken checks the signature, audience, and expiry and returns the
// invited email. Any failure is a single error so callers reject uniformly.
func VerifyInviteToken(secret, token string) (string, error) {
	return parseAudienceToken(secret, token, inviteAudience)
}
