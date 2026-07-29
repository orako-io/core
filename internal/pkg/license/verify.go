// SPDX-License-Identifier: AGPL-3.0-or-later

// Package license provides offline Ed25519 license verification. No network
// call, ever — and the seam is not a gate: the server runs without a valid
// license; failures are logged, never fatal.
package license

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrInvalidSignature is returned when the envelope's Ed25519 signature does
// not verify against the supplied public key.
var ErrInvalidSignature = errors.New("license: invalid signature")

// ErrExpired is returned when a non-zero ExpiresAt has passed.
var ErrExpired = errors.New("license: expired")

// License holds the decoded, verified fields of an Orako license.
type License struct {
	// Subject is the licensee identifier (organisation ID or name).
	Subject string `json:"sub"`
	// Seats is the number of licensed member seats (per org). Zero means unlimited.
	Seats int `json:"seats"`
	// MaxOrgs is the number of organisations the license permits. Zero means
	// unlimited. Absent in older tokens (decodes to zero → unlimited).
	MaxOrgs int `json:"max_orgs,omitempty"`
	// MaxProjects is the number of projects permitted per org. Zero means
	// unlimited.
	MaxProjects int `json:"max_projects,omitempty"`
	// Features is the list of enabled feature flags for this license tier.
	Features []string `json:"features,omitempty"`
	// ExpiresAt is the UTC expiry timestamp. A zero value means perpetual.
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	// IssuedAt is the UTC timestamp when the license was signed.
	IssuedAt time.Time `json:"issued_at"`
}

// Envelope is the on-disk / env-var wire format: Payload is the raw JSON bytes
// of a License, Signature the Ed25519 signature over those exact bytes.
// encoding/json marshals []byte as base64, so the wire uses base64 strings.
type Envelope struct {
	Payload   []byte `json:"payload"`
	Signature []byte `json:"signature"`
}

// Verify decodes envelopeJSON, verifies the Ed25519 signature against
// publicKey, checks expiry, and returns the decoded License. It returns
// [ErrInvalidSignature] or [ErrExpired] respectively. No network call is made;
// safe for concurrent use.
func Verify(envelopeJSON []byte, publicKey ed25519.PublicKey) (License, error) {
	// ed25519.Verify PANICS on a wrong-length key; guard so a malformed
	// A key from another signer is rejected rather than causing a boot crash.
	if len(publicKey) != ed25519.PublicKeySize {
		return License{}, ErrInvalidSignature
	}

	var env Envelope
	if err := json.Unmarshal(envelopeJSON, &env); err != nil {
		return License{}, fmt.Errorf("license: decoding envelope: %w", err)
	}

	if !ed25519.Verify(publicKey, env.Payload, env.Signature) {
		return License{}, ErrInvalidSignature
	}

	var lic License
	if err := json.Unmarshal(env.Payload, &lic); err != nil {
		return License{}, fmt.Errorf("license: decoding license payload: %w", err)
	}

	if !lic.ExpiresAt.IsZero() && time.Now().UTC().After(lic.ExpiresAt) {
		return License{}, ErrExpired
	}

	return lic, nil
}
