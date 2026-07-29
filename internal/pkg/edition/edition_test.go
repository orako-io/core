// SPDX-License-Identifier: AGPL-3.0-or-later

package edition_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/orako-io/core/internal/pkg/edition"
	"github.com/orako-io/core/internal/pkg/license"
)

// signLicense signs lic with priv and returns the base64 envelope + base64
// public key, exactly as the DB-stored key and baked public key are supplied to
// Resolve.
func signLicense(t *testing.T, lic license.License, pub ed25519.PublicKey, priv ed25519.PrivateKey) (keyB64, pubB64 string) {
	t.Helper()

	payload, err := json.Marshal(lic)
	if err != nil {
		t.Fatalf("marshal license: %v", err)
	}

	env := license.Envelope{Payload: payload, Signature: ed25519.Sign(priv, payload)}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	return base64.StdEncoding.EncodeToString(data), base64.StdEncoding.EncodeToString(pub)
}

// TestResolveSaaSIgnoresEverything proves billing-enabled always yields SaaS
// with no community caps, even when a license key is also present.
func TestResolveSaaSIgnoresEverything(t *testing.T) {
	t.Parallel()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	keyB64, pubB64 := signLicense(t, license.License{Seats: 3}, pub, priv)

	e, err := edition.Resolve(true, keyB64, pubB64)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if e.Kind != edition.SaaS {
		t.Errorf("Kind = %s, want saas", e.Kind)
	}

	if e.Enforced() {
		t.Error("SaaS must not enforce community caps")
	}

	if (e.Limits != edition.Limits{}) {
		t.Errorf("SaaS limits = %+v, want all-unlimited zero value", e.Limits)
	}
}

// TestResolveCommunityDefault proves that with no billing and no key the
// edition is Community with the 5/1/1 caps and enforcement on.
func TestResolveCommunityDefault(t *testing.T) {
	t.Parallel()

	e, err := edition.Resolve(false, "", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if e.Kind != edition.Community {
		t.Errorf("Kind = %s, want community", e.Kind)
	}

	if !e.Enforced() {
		t.Error("Community must enforce caps")
	}

	if e.Limits != (edition.Limits{MaxMembers: 5, MaxOrgs: 1, MaxProjects: 1}) {
		t.Errorf("Community limits = %+v, want 5/1/1", e.Limits)
	}
}

// TestResolveLicensedUsesTokenLimits proves a valid key yields Licensed with
// the token's limits and features, and no community enforcement.
func TestResolveLicensedUsesTokenLimits(t *testing.T) {
	t.Parallel()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	lic := license.License{
		Subject:     "acme",
		Seats:       25,
		MaxOrgs:     5,
		MaxProjects: 50,
		Features:    []string{"sso", "audit_log"},
		IssuedAt:    time.Now().UTC().Add(-time.Hour),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour),
	}

	keyB64, pubB64 := signLicense(t, lic, pub, priv)

	e, err := edition.Resolve(false, keyB64, pubB64)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if e.Kind != edition.Licensed {
		t.Fatalf("Kind = %s, want licensed", e.Kind)
	}

	if !e.Enforced() {
		t.Error("Licensed must enforce its token limits (seats/orgs/projects)")
	}

	if e.Limits != (edition.Limits{MaxMembers: 25, MaxOrgs: 5, MaxProjects: 50}) {
		t.Errorf("limits = %+v, want 25/5/50", e.Limits)
	}

	if !e.Allows("sso") || e.Allows("nope") {
		t.Error("Allows did not reflect token features")
	}
}

// TestResolveInvalidKeyFallsBackToCommunity proves that a key signed by the
// wrong private key does NOT unlock anything: it returns Community + an error.
func TestResolveInvalidKeyFallsBackToCommunity(t *testing.T) {
	t.Parallel()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	_, wrongPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate wrong key: %v", err)
	}

	// Sign with the wrong private key but advertise the real public key.
	keyB64, pubB64 := signLicense(t, license.License{Seats: 999}, pub, wrongPriv)

	e, err := edition.Resolve(false, keyB64, pubB64)
	if err == nil {
		t.Fatal("expected an error for an unverifiable key")
	}

	if e.Kind != edition.Community {
		t.Errorf("Kind = %s, want community fallback", e.Kind)
	}

	if e.Limits != edition.CommunityLimits {
		t.Errorf("limits = %+v, want community caps on invalid key", e.Limits)
	}
}

// TestResolveExpiredKeyFallsBackToCommunity proves an expired token does not
// unlock; it returns Community + an error.
func TestResolveExpiredKeyFallsBackToCommunity(t *testing.T) {
	t.Parallel()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	lic := license.License{
		Subject:   "acme",
		Seats:     25,
		IssuedAt:  time.Now().UTC().Add(-48 * time.Hour),
		ExpiresAt: time.Now().UTC().Add(-time.Hour),
	}

	keyB64, pubB64 := signLicense(t, lic, pub, priv)

	e, err := edition.Resolve(false, keyB64, pubB64)
	if err == nil {
		t.Fatal("expected an error for an expired key")
	}

	if e.Kind != edition.Community {
		t.Errorf("Kind = %s, want community fallback", e.Kind)
	}
}

// TestResolveGarbageKeyFallsBackToCommunity proves malformed base64/pubkey
// inputs degrade to Community rather than panicking.
func TestResolveGarbageKeyFallsBackToCommunity(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ key, pub string }{
		"bad base64 key": {"!!!not base64!!!", base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize))},
		"bad base64 pub": {base64.StdEncoding.EncodeToString([]byte("{}")), "!!!"},
		"short pubkey":   {base64.StdEncoding.EncodeToString([]byte("{}")), base64.StdEncoding.EncodeToString([]byte("tooshort"))},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e, err := edition.Resolve(false, tc.key, tc.pub)
			if err == nil {
				t.Fatal("expected an error for garbage input")
			}

			if e.Kind != edition.Community {
				t.Errorf("Kind = %s, want community fallback", e.Kind)
			}
		})
	}
}
