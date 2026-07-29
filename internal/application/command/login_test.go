// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/auth"
	"github.com/orako-io/core/internal/pkg/errs"
)

type fakeCreds struct {
	accountID uuid.UUID
	hash      string
	ok        bool
	err       error
}

func (f fakeCreds) CredentialByEmail(context.Context, string) (uuid.UUID, string, bool, error) {
	return f.accountID, f.hash, f.ok, f.err
}

const (
	secret = "test-hs256-secret-at-least-32-bytes!!"
	issuer = "orako"
	aud    = "orako-dashboard"
)

func TestLogin_ValidCredentials_MintsVerifiableToken(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	hash, _ := auth.HashPassword("correct-password")

	h := NewLoginHandler(fakeCreds{accountID: accountID, hash: hash, ok: true}, secret, issuer, aud, time.Hour)

	tok, err := h.Login(context.Background(), "sam@example.com", "correct-password")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// The token must verify through the same verifier the server uses, and carry
	// the account's local subject.
	v, _ := auth.NewHS256Verifier(secret, issuer, aud)

	claims, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("minted token did not verify: %v", err)
	}

	if claims.Subject != LocalSubject(accountID) {
		t.Errorf("subject = %q, want %q", claims.Subject, LocalSubject(accountID))
	}
}

func TestLogin_WrongPassword_And_UnknownEmail_SameError(t *testing.T) {
	t.Parallel()

	hash, _ := auth.HashPassword("correct-password")

	// wrong password (account exists)
	h1 := NewLoginHandler(fakeCreds{accountID: uuid.New(), hash: hash, ok: true}, secret, issuer, aud, time.Hour)
	_, err1 := h1.Login(context.Background(), "sam@example.com", "WRONG")

	// unknown email / no local password (ok=false)
	h2 := NewLoginHandler(fakeCreds{ok: false}, secret, issuer, aud, time.Hour)
	_, err2 := h2.Login(context.Background(), "ghost@example.com", "whatever")

	for i, err := range []error{err1, err2} {
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("case %d: got %v, want ErrInvalidCredentials", i, err)
		}
	}
}

func TestLogin_StoreError_IsInternal(t *testing.T) {
	t.Parallel()

	h := NewLoginHandler(fakeCreds{err: errors.New("db down")}, secret, issuer, aud, time.Hour)

	_, err := h.Login(context.Background(), "sam@example.com", "pw")

	var internal errs.InternalError
	if !errors.As(err, &internal) {
		t.Fatalf("got %T (%v), want errs.InternalError", err, err)
	}
}
