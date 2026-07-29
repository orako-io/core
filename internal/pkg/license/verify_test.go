// SPDX-License-Identifier: AGPL-3.0-or-later

package license_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/orako-io/core/internal/pkg/license"
)

// makeEnvelope signs lic with priv and returns the JSON-encoded Envelope.
func makeEnvelope(t *testing.T, lic license.License, priv ed25519.PrivateKey) []byte {
	t.Helper()

	payload, err := json.Marshal(lic)
	if err != nil {
		t.Fatalf("marshal license: %v", err)
	}

	sig := ed25519.Sign(priv, payload)

	env := license.Envelope{Payload: payload, Signature: sig}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	return data
}

// TestVerifyValid proves a correctly signed, non-expired license is accepted
// and the decoded fields match the original.
func TestVerifyValid(t *testing.T) {
	t.Parallel()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	lic := license.License{
		Subject:   "acme-corp",
		Seats:     25,
		Features:  []string{"kb", "slack"},
		IssuedAt:  time.Now().UTC().Add(-time.Hour),
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	}

	data := makeEnvelope(t, lic, priv)

	got, err := license.Verify(data, pub)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if got.Subject != "acme-corp" {
		t.Errorf("Subject: got %q, want %q", got.Subject, "acme-corp")
	}

	if got.Seats != 25 {
		t.Errorf("Seats: got %d, want 25", got.Seats)
	}

	if len(got.Features) != 2 {
		t.Errorf("Features: got %v, want [kb slack]", got.Features)
	}
}

// TestVerifyPerpetual proves a license with a zero ExpiresAt (no expiry) is
// accepted regardless of how much time has passed.
func TestVerifyPerpetual(t *testing.T) {
	t.Parallel()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	lic := license.License{
		Subject:  "acme-corp",
		IssuedAt: time.Now().UTC().Add(-365 * 24 * time.Hour),
		// ExpiresAt is zero: perpetual
	}

	data := makeEnvelope(t, lic, priv)

	_, err = license.Verify(data, pub)
	if err != nil {
		t.Fatalf("Verify (perpetual): %v", err)
	}
}

// TestVerifyExpired proves a license with ExpiresAt in the past is rejected
// with ErrExpired.
func TestVerifyExpired(t *testing.T) {
	t.Parallel()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	lic := license.License{
		Subject:   "acme-corp",
		IssuedAt:  time.Now().UTC().Add(-48 * time.Hour),
		ExpiresAt: time.Now().UTC().Add(-time.Hour),
	}

	data := makeEnvelope(t, lic, priv)

	_, err = license.Verify(data, pub)
	if !errors.Is(err, license.ErrExpired) {
		t.Fatalf("Verify (expired): got %v, want ErrExpired", err)
	}
}

// TestVerifyInvalidSignature proves that a license signed with a different
// private key is rejected with ErrInvalidSignature.
func TestVerifyInvalidSignature(t *testing.T) {
	t.Parallel()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	_, wrongPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate wrong key: %v", err)
	}

	lic := license.License{
		Subject:  "acme-corp",
		IssuedAt: time.Now().UTC(),
	}

	data := makeEnvelope(t, lic, wrongPriv)

	_, err = license.Verify(data, pub)
	if !errors.Is(err, license.ErrInvalidSignature) {
		t.Fatalf("Verify (tampered): got %v, want ErrInvalidSignature", err)
	}
}

// TestVerifyTamperedPayload proves that modifying the payload after signing is
// rejected with ErrInvalidSignature.
func TestVerifyTamperedPayload(t *testing.T) {
	t.Parallel()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	lic := license.License{
		Subject:  "acme-corp",
		Seats:    1,
		IssuedAt: time.Now().UTC(),
	}

	data := makeEnvelope(t, lic, priv)

	// Decode, tamper with the payload, re-encode.
	var env license.Envelope
	if err = json.Unmarshal(data, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Flip a byte in the raw payload to simulate tampering.
	if len(env.Payload) > 5 {
		env.Payload[5] ^= 0xFF
	}

	tampered, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal tampered: %v", err)
	}

	_, err = license.Verify(tampered, pub)
	if !errors.Is(err, license.ErrInvalidSignature) {
		t.Fatalf("Verify (tampered payload): got %v, want ErrInvalidSignature", err)
	}
}

// TestVerifyMalformedJSON proves that garbage JSON returns a wrapped error
// that is neither ErrExpired nor ErrInvalidSignature.
func TestVerifyMalformedJSON(t *testing.T) {
	t.Parallel()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	_, err = license.Verify([]byte("not-json"), pub)
	if err == nil {
		t.Fatal("Verify (malformed): expected error, got nil")
	}

	if errors.Is(err, license.ErrExpired) || errors.Is(err, license.ErrInvalidSignature) {
		t.Fatalf("Verify (malformed): got sentinel %v, want wrapped parse error", err)
	}
}
