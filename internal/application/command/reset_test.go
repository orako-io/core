// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/auth"
)

// fakeResetStore satisfies the store port the reset handler needs.
type fakeResetStore struct {
	mu          sync.Mutex
	account     uuid.UUID
	version     int
	ok          bool
	lookupErr   error
	resetErr    error
	passwordSet int
}

func (f *fakeResetStore) ResetVersionByEmail(context.Context, string) (uuid.UUID, int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.account, f.version, f.ok, f.lookupErr
}

func (f *fakeResetStore) ResetPassword(_ context.Context, _ string, expectedVersion int, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.resetErr != nil {
		return false, f.resetErr
	}

	if !f.ok || expectedVersion != f.version {
		return false, nil
	}

	f.passwordSet++
	f.version++

	return true, nil
}

func (f *fakeResetStore) passwordSetCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.passwordSet
}

// fakeMailer records sends. RequestReset dispatches asynchronously (M3), so the
// counter is mutex-guarded and tests poll it.
type fakeMailer struct {
	mu   sync.Mutex
	sent int
	last model.EmailMessage
}

func (m *fakeMailer) Send(_ context.Context, msg model.EmailMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent++
	m.last = msg
	return nil
}

func (m *fakeMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sent
}

// waitSent polls until at least n messages are sent or the deadline passes.
func (m *fakeMailer) waitSent(n int) bool {
	for range 100 {
		if m.count() >= n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestRequestReset_KnownAccount_SendsEmail(t *testing.T) {
	t.Parallel()

	store := &fakeResetStore{account: uuid.New(), ok: true}
	mail := &fakeMailer{}
	h := NewResetHandler(store, mail, "secret", "https://app.example.com", time.Hour, nil)

	h.RequestReset(context.Background(), "user@example.com")

	if !mail.waitSent(1) {
		t.Fatalf("mailer sent = %d, want 1", mail.count())
	}

	mail.mu.Lock()
	last := mail.last
	mail.mu.Unlock()

	if last.To != "user@example.com" {
		t.Errorf("email To = %q, want user@example.com", last.To)
	}
	if last.Subject == "" {
		t.Error("reset email has no subject")
	}
}

func TestRequestReset_UnknownAccount_NoEmail(t *testing.T) {
	t.Parallel()

	store := &fakeResetStore{ok: false}
	mail := &fakeMailer{}
	h := NewResetHandler(store, mail, "secret", "https://app.example.com", time.Hour, nil)

	h.RequestReset(context.Background(), "ghost@example.com")

	// Async: give the goroutine time to (not) run, then assert nothing was sent.
	time.Sleep(80 * time.Millisecond)
	if mail.count() != 0 {
		t.Errorf("no email should be sent for an unknown account, sent=%d", mail.count())
	}
}

func TestPerformReset_ValidToken_SetsPassword(t *testing.T) {
	t.Parallel()

	const secret = "secret"

	store := &fakeResetStore{account: uuid.New(), ok: true}
	h := NewResetHandler(store, &fakeMailer{}, secret, "https://app.example.com", time.Hour, nil)

	tok, _ := auth.MintResetToken(secret, "user@example.com", 0, time.Hour, time.Now())

	if err := h.PerformReset(context.Background(), tok, "newpassword123"); err != nil {
		t.Fatalf("PerformReset: %v", err)
	}
	if store.passwordSetCount() != 1 {
		t.Errorf("passwordSet = %d, want 1", store.passwordSetCount())
	}
}

// A token minted against an old version (already spent, or superseded by a later
// password change) is rejected — single-use (L1).
func TestPerformReset_StaleVersion(t *testing.T) {
	t.Parallel()

	const secret = "secret"

	// Store is now at version 1; the token was minted at version 0.
	store := &fakeResetStore{account: uuid.New(), version: 1, ok: true}
	h := NewResetHandler(store, &fakeMailer{}, secret, "https://app.example.com", time.Hour, nil)

	staleTok, _ := auth.MintResetToken(secret, "user@example.com", 0, time.Hour, time.Now())

	if err := h.PerformReset(context.Background(), staleTok, "newpassword123"); !errors.Is(err, ErrInvalidReset) {
		t.Errorf("stale token: got %v, want ErrInvalidReset", err)
	}
	if store.passwordSetCount() != 0 {
		t.Error("must not set a password for a stale (already-used) token")
	}
}

func TestPerformReset_ConcurrentReplayOnlyOneSucceeds(t *testing.T) {
	t.Parallel()

	const secret = "secret"

	store := &fakeResetStore{account: uuid.New(), ok: true}
	h := NewResetHandler(store, &fakeMailer{}, secret, "https://app.example.com", time.Hour, nil)

	token, _ := auth.MintResetToken(secret, "user@example.com", 0, time.Hour, time.Now())

	start := make(chan struct{})
	results := make(chan error, 2)

	for _, password := range []string{"newpassword123", "otherpassword456"} {
		go func() {
			<-start
			results <- h.PerformReset(context.Background(), token, password)
		}()
	}

	close(start)

	var (
		succeeded int
		rejected  int
	)

	for range 2 {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrInvalidReset):
			rejected++
		default:
			t.Fatalf("unexpected concurrent reset result: %v", err)
		}
	}

	if succeeded != 1 || rejected != 1 {
		t.Fatalf("concurrent reset results: succeeded=%d rejected=%d, want 1/1", succeeded, rejected)
	}

	if store.passwordSetCount() != 1 {
		t.Fatalf("password writes = %d, want 1", store.passwordSetCount())
	}
}

func TestPerformReset_Rejects(t *testing.T) {
	t.Parallel()

	const secret = "secret"

	tok, _ := auth.MintResetToken(secret, "user@example.com", 0, time.Hour, time.Now())

	// Bad token → ErrInvalidReset, no write.
	store := &fakeResetStore{account: uuid.New(), ok: true}
	h := NewResetHandler(store, &fakeMailer{}, secret, "https://app.example.com", time.Hour, nil)
	if err := h.PerformReset(context.Background(), "garbage", "newpassword123"); !errors.Is(err, ErrInvalidReset) {
		t.Errorf("bad token: got %v, want ErrInvalidReset", err)
	}
	if store.passwordSetCount() != 0 {
		t.Error("must not set a password on a bad token")
	}

	// Short password → validation error, no write.
	store2 := &fakeResetStore{account: uuid.New(), ok: true}
	h2 := NewResetHandler(store2, &fakeMailer{}, secret, "https://app.example.com", time.Hour, nil)
	if err := h2.PerformReset(context.Background(), tok, "short"); err == nil {
		t.Error("short password must be rejected")
	}
	if store2.passwordSetCount() != 0 {
		t.Error("must not set a weak password")
	}

	// Valid token but the account is gone (ok=false) → ErrInvalidReset.
	store3 := &fakeResetStore{ok: false}
	h3 := NewResetHandler(store3, &fakeMailer{}, secret, "https://app.example.com", time.Hour, nil)
	if err := h3.PerformReset(context.Background(), tok, "newpassword123"); !errors.Is(err, ErrInvalidReset) {
		t.Errorf("gone account: got %v, want ErrInvalidReset", err)
	}
}
