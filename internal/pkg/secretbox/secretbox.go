// SPDX-License-Identifier: Apache-2.0

// Package secretbox encrypts secret blobs (integration credentials) at rest with
// AES-256-GCM. Encryption is opt-in: with no key configured a nil Cipher passes
// values through as plaintext, and a value written before encryption was enabled
// is read back transparently — so turning it on is a safe, no-downtime rollout.
package secretbox

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
)

// EnvKey is the environment variable holding the base64-encoded 32-byte key.
const EnvKey = "ORAKO_ENCRYPTION_KEY"

// magic prefixes every value this package seals, marking it distinct from a
// legacy plaintext JSON blob (whose first byte is '{', 0x7B). 0x01 can never
// begin JSON, so the two are unambiguous on read.
var magic = []byte{0x01, 'O', 'R', 'K', '1'} //nolint:gochecknoglobals // immutable format marker

// Cipher seals and opens secret blobs. The zero value is unusable; build one
// with New or NewFromEnv. A nil *Cipher is a valid no-op (encryption disabled).
type Cipher struct {
	aead cipher.AEAD
}

// NewFromEnv builds a Cipher from ORAKO_ENCRYPTION_KEY (base64 of 32 bytes). It
// returns (nil, nil) when the key is unset: encryption is opt-in and a nil
// Cipher stores/returns values as plaintext (legacy behavior).
func NewFromEnv() (*Cipher, error) {
	raw := os.Getenv(EnvKey)
	if raw == "" {
		return nil, nil //nolint:nilnil // nil Cipher is the documented "encryption disabled" value
	}

	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %s must be base64: %w", EnvKey, err)
	}

	return New(key)
}

// New builds a Cipher from a raw 32-byte key.
func New(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secretbox: key must be 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: building cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: building GCM: %w", err)
	}

	return &Cipher{aead: aead}, nil
}

// Enabled reports whether c will actually encrypt (a nil Cipher does not).
func (c *Cipher) Enabled() bool {
	return c != nil
}

// Encrypt seals plaintext as magic || nonce || ciphertext. A nil Cipher returns
// plaintext unchanged (encryption disabled).
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	if c == nil {
		return plaintext, nil
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("secretbox: reading nonce: %w", err)
	}

	sealed := c.aead.Seal(nil, nonce, plaintext, nil)

	out := make([]byte, 0, len(magic)+len(nonce)+len(sealed))
	out = append(out, magic...)
	out = append(out, nonce...)
	out = append(out, sealed...)

	return out, nil
}

// Decrypt opens a value produced by Encrypt. A value without the magic prefix is
// returned unchanged — a legacy plaintext blob written before encryption was
// enabled. A prefixed value with no key configured is an error (the data needs
// the key that sealed it).
func (c *Cipher) Decrypt(blob []byte) ([]byte, error) {
	if !bytes.HasPrefix(blob, magic) {
		return blob, nil // legacy plaintext
	}

	if c == nil {
		return nil, errors.New("secretbox: value is encrypted but no key is configured")
	}

	body := blob[len(magic):]

	nonceSize := c.aead.NonceSize()
	if len(body) < nonceSize {
		return nil, errors.New("secretbox: ciphertext too short")
	}

	nonce, ciphertext := body[:nonceSize], body[nonceSize:]

	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("secretbox: opening ciphertext: %w", err)
	}

	return plaintext, nil
}
