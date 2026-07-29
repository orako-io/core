// SPDX-License-Identifier: Apache-2.0

package secretbox_test

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"

	"github.com/orako-io/core/internal/pkg/secretbox"
)

func newKey(t *testing.T) []byte {
	t.Helper()

	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatalf("generating key: %v", err)
	}

	return key
}

func TestCipher_RoundTrip(t *testing.T) {
	t.Parallel()

	c, err := secretbox.New(newKey(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	plaintext := []byte(`{"bot_token":"xoxb-secret","signing_secret":"abc"}`)

	sealed, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if bytes.Contains(sealed, []byte("xoxb-secret")) {
		t.Fatal("ciphertext still contains the plaintext token")
	}

	opened, err := c.Decrypt(sealed)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if !bytes.Equal(opened, plaintext) {
		t.Errorf("round trip = %q, want %q", opened, plaintext)
	}
}

func TestCipher_EncryptIsNonDeterministic(t *testing.T) {
	t.Parallel()

	c, _ := secretbox.New(newKey(t))
	plaintext := []byte("same input")

	a, _ := c.Encrypt(plaintext)
	b, _ := c.Encrypt(plaintext)

	if bytes.Equal(a, b) {
		t.Error("two encryptions produced identical ciphertext (nonce reuse)")
	}
}

// A value written before encryption was enabled has no magic prefix and must
// read back unchanged, so enabling the key never orphans existing rows.
func TestCipher_DecryptPassesThroughLegacyPlaintext(t *testing.T) {
	t.Parallel()

	c, _ := secretbox.New(newKey(t))
	legacy := []byte(`{"bot_token":"legacy-plaintext"}`)

	out, err := c.Decrypt(legacy)
	if err != nil {
		t.Fatalf("Decrypt legacy: %v", err)
	}

	if !bytes.Equal(out, legacy) {
		t.Errorf("legacy passthrough = %q, want %q", out, legacy)
	}
}

// A nil Cipher (no key configured) is a no-op: plaintext in, plaintext out.
func TestNilCipher_IsPlaintextNoOp(t *testing.T) {
	t.Parallel()

	var c *secretbox.Cipher

	if c.Enabled() {
		t.Error("nil Cipher reports Enabled")
	}

	plaintext := []byte("hello")

	sealed, err := c.Encrypt(plaintext)
	if err != nil || !bytes.Equal(sealed, plaintext) {
		t.Errorf("nil Encrypt = %q, %v; want passthrough", sealed, err)
	}

	out, err := c.Decrypt(plaintext)
	if err != nil || !bytes.Equal(out, plaintext) {
		t.Errorf("nil Decrypt = %q, %v; want passthrough", out, err)
	}
}

// A nil Cipher cannot open a value that WAS encrypted — it must error, not
// silently return ciphertext as if it were plaintext.
func TestNilCipher_RejectsEncryptedValue(t *testing.T) {
	t.Parallel()

	c, _ := secretbox.New(newKey(t))
	sealed, _ := c.Encrypt([]byte("secret"))

	var nilCipher *secretbox.Cipher
	if _, err := nilCipher.Decrypt(sealed); err == nil {
		t.Error("nil Cipher must reject an encrypted value, got nil error")
	}
}

func TestNew_RejectsWrongKeySize(t *testing.T) {
	t.Parallel()

	if _, err := secretbox.New([]byte("too-short")); err == nil {
		t.Error("expected an error for a non-32-byte key")
	}
}

func TestCipher_DecryptRejectsTampered(t *testing.T) {
	t.Parallel()

	c, _ := secretbox.New(newKey(t))
	sealed, _ := c.Encrypt([]byte("secret"))

	sealed[len(sealed)-1] ^= 0xFF // flip a ciphertext bit

	if _, err := c.Decrypt(sealed); err == nil {
		t.Error("expected an authentication error on tampered ciphertext")
	}
}
