// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/auth"
	"github.com/orako-io/core/internal/pkg/errs"
)

// minPasswordLen is the shortest password AcceptInvite accepts.
const minPasswordLen = 8

// fieldPassword names the password field in validation errors.
const fieldPassword = "password"

// ErrInvalidInvite is returned for a bad/expired invite token or an already-used
// invitation — uniform, so it cannot probe which emails were invited.
var ErrInvalidInvite = errs.InvalidError{Field: "invite", Reason: "invalid or already-used invitation"}

// AcceptInviteHandler turns a valid invite token + a chosen password into a local
// account. The invite-created member is linked to it by email on first login
// (the principal resolver's by-email fallback).
type AcceptInviteHandler struct {
	accounts accountSeedStore
	txor     service.Transactor
	secret   string
}

// NewAcceptInviteHandler builds the handler. secret must be ORAKO_AUTH_HS256_SECRET.
func NewAcceptInviteHandler(accounts accountSeedStore, txor service.Transactor, secret string) AcceptInviteHandler {
	if accounts == nil || txor == nil {
		panic("AcceptInviteHandler requires non-nil dependencies")
	}

	return AcceptInviteHandler{accounts: accounts, txor: txor, secret: secret}
}

// AcceptInvite verifies the invite token, then creates a local account with the
// chosen password. Rejects an unverifiable/expired token or an email that already
// has an account (already accepted).
func (h AcceptInviteHandler) AcceptInvite(ctx context.Context, token, password string) error {
	if len(password) < minPasswordLen {
		return errs.InvalidError{Field: fieldPassword, Reason: fmt.Sprintf("password must be at least %d characters", minPasswordLen)}
	}

	email, err := auth.VerifyInviteToken(h.secret, token)
	if err != nil {
		return ErrInvalidInvite
	}

	switch _, lookupErr := h.accounts.ByEmail(ctx, email); {
	case lookupErr == nil:
		return ErrInvalidInvite // already accepted
	case errors.Is(lookupErr, adaptererr.ErrNotFound):
		// fall through and create
	default:
		return errs.InternalError{Err: fmt.Errorf("checking existing account: %w", lookupErr)}
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return errs.InvalidError{Field: fieldPassword, Reason: "password is too long (max 72 bytes)"}
	}

	id := uuid.New()

	account, err := model.NewAccount(id, email, LocalSubject(id), "")
	if err != nil {
		return errs.InternalError{Err: fmt.Errorf("building account: %w", err)}
	}

	if err := h.txor.WithTx(ctx, func(ctx context.Context) error {
		if err := h.accounts.Create(ctx, account); err != nil {
			return fmt.Errorf("creating account: %w", err)
		}

		if err := h.accounts.SetPassword(ctx, id, hash); err != nil {
			return fmt.Errorf("setting password: %w", err)
		}

		return nil
	}); err != nil {
		return errs.InternalError{Err: err}
	}

	return nil
}
