// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/auth"
)

// fakeAccounts satisfies the account port AcceptInvite needs.
type fakeAccounts struct {
	existing    bool
	created     int
	passwordSet int
}

func (f *fakeAccounts) ByEmail(context.Context, string) (model.Account, error) {
	if f.existing {
		return model.Account{ID: uuid.New(), Email: "x@x.com"}, nil
	}

	return model.Account{}, adaptererr.ErrNotFound
}

func (f *fakeAccounts) Create(context.Context, model.Account) error {
	f.created++

	return nil
}

func (f *fakeAccounts) SetPassword(context.Context, uuid.UUID, string) error {
	f.passwordSet++

	return nil
}

func TestAcceptInvite_Valid_CreatesAccountWithPassword(t *testing.T) {
	t.Parallel()

	const secret = "s"

	tok, _ := auth.MintInviteToken(secret, "new@example.com", time.Hour, time.Now())
	acc := &fakeAccounts{}
	h := NewAcceptInviteHandler(acc, secret)

	if err := h.AcceptInvite(context.Background(), tok, "password123"); err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}

	if acc.created != 1 || acc.passwordSet != 1 {
		t.Errorf("want one account created + password set, got created=%d passwordSet=%d", acc.created, acc.passwordSet)
	}
}

func TestAcceptInvite_Rejects(t *testing.T) {
	t.Parallel()

	const secret = "s"

	tok, _ := auth.MintInviteToken(secret, "e@example.com", time.Hour, time.Now())

	// Bad token → ErrInvalidInvite, nothing created.
	acc := &fakeAccounts{}
	if err := NewAcceptInviteHandler(acc, secret).AcceptInvite(context.Background(), "garbage", "password123"); !errors.Is(err, ErrInvalidInvite) {
		t.Errorf("bad token: got %v, want ErrInvalidInvite", err)
	}

	if acc.created != 0 {
		t.Error("must not create an account on a bad token")
	}

	// Short password → validation error, nothing created.
	acc2 := &fakeAccounts{}
	if err := NewAcceptInviteHandler(acc2, secret).AcceptInvite(context.Background(), tok, "short"); err == nil {
		t.Error("short password must be rejected")
	}

	if acc2.created != 0 {
		t.Error("must not create an account on a weak password")
	}

	// Already accepted (account exists) → ErrInvalidInvite, nothing created.
	acc3 := &fakeAccounts{existing: true}
	if err := NewAcceptInviteHandler(acc3, secret).AcceptInvite(context.Background(), tok, "password123"); !errors.Is(err, ErrInvalidInvite) {
		t.Errorf("already accepted: got %v, want ErrInvalidInvite", err)
	}

	if acc3.created != 0 {
		t.Error("must not re-create an existing account")
	}
}
