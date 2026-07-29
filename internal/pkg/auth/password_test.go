// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"strings"
	"testing"

	"github.com/orako-io/core/internal/pkg/auth"
)

func TestHashPassword(t *testing.T) {
	t.Parallel()

	hash, err := auth.HashPassword("correct horse battery")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if hash == "correct horse battery" || hash == "" {
		t.Fatal("hash must not be empty or the plaintext")
	}
}

func TestHashPasswordRejectsOverLength(t *testing.T) {
	t.Parallel()

	// 73 bytes: past bcrypt's 72-byte limit — must error, not silently truncate.
	if _, err := auth.HashPassword(strings.Repeat("a", 73)); err == nil {
		t.Error("over-length password must be rejected")
	}
}
