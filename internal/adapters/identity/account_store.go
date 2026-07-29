// SPDX-License-Identifier: AGPL-3.0-or-later

package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/pgconv"
	postgres "github.com/orako-io/core/internal/pkg/postgres"
)

// Compile-time assertion that AccountStore satisfies the AccountRepository port.
var _ repository.AccountRepository = (*AccountStore)(nil)

// AccountStore is the Postgres-backed AccountRepository.
type AccountStore struct {
	pool *pgxpool.Pool
}

// NewAccountStore builds an AccountStore backed by pool.
func NewAccountStore(pool *pgxpool.Pool) *AccountStore {
	return &AccountStore{pool: pool}
}

// Create durably stores a new account.
func (s *AccountStore) Create(ctx context.Context, account model.Account) error {
	_, err := New(postgres.Conn(ctx, s.pool)).createAccount(ctx, createAccountParams{
		ID:          account.ID,
		Subject:     pgconv.TextOrNull(account.Subject),
		Email:       account.Email,
		DisplayName: pgconv.TextOrNull(account.DisplayName),
	})
	if err != nil {
		return fmt.Errorf("creating account: %w", adaptererr.Decode(err))
	}

	return nil
}

// SetPassword stores the bcrypt password hash for a local-auth account (empty
// hash clears it, e.g. when the account switches to an external IdP).
func (s *AccountStore) SetPassword(ctx context.Context, accountID uuid.UUID, hash string) error {
	if err := New(postgres.Conn(ctx, s.pool)).setAccountPassword(ctx, setAccountPasswordParams{
		ID:           accountID,
		PasswordHash: pgconv.TextOrNull(hash),
	}); err != nil {
		return fmt.Errorf("setting account password: %w", adaptererr.Decode(err))
	}

	return nil
}

// CredentialByEmail returns the account id + bcrypt hash for an email/password
// login. ok is false when no account has that email OR the account has no local
// password (an IdP-only account) — both cases the login must reject uniformly so
// it cannot be used to probe which emails exist.
func (s *AccountStore) CredentialByEmail(ctx context.Context, email string) (accountID uuid.UUID, hash string, ok bool, err error) {
	row, qerr := New(postgres.Conn(ctx, s.pool)).accountCredentialByEmail(ctx, email)
	if qerr != nil {
		if decoded := adaptererr.Decode(qerr); errors.Is(decoded, adaptererr.ErrNotFound) {
			return uuid.Nil, "", false, nil
		}

		return uuid.Nil, "", false, fmt.Errorf("loading account credential: %w", adaptererr.Decode(qerr))
	}

	if !row.PasswordHash.Valid {
		return row.ID, "", false, nil
	}

	return row.ID, row.PasswordHash.String, true, nil
}

// ResetVersionByEmail returns the account id + current password_reset_version for
// a local account. ok is false for an unknown email or an IdP-only account (no
// local password) — same uniform semantics as CredentialByEmail.
func (s *AccountStore) ResetVersionByEmail(ctx context.Context, email string) (accountID uuid.UUID, version int, ok bool, err error) {
	row, qerr := New(postgres.Conn(ctx, s.pool)).accountResetVersionByEmail(ctx, email)
	if qerr != nil {
		if decoded := adaptererr.Decode(qerr); errors.Is(decoded, adaptererr.ErrNotFound) {
			return uuid.Nil, 0, false, nil
		}

		return uuid.Nil, 0, false, fmt.Errorf("loading account reset version: %w", adaptererr.Decode(qerr))
	}

	return row.ID, int(row.PasswordResetVersion), true, nil
}

// ResetPassword atomically replaces a local account's password and spends the
// expected reset version. updated is false for an unknown/IdP-only account or
// when another request already spent the same reset token.
func (s *AccountStore) ResetPassword(
	ctx context.Context,
	email string,
	expectedVersion int,
	hash string,
) (updated bool, err error) {
	err = postgres.NewTransactor(s.pool).WithTx(ctx, func(txCtx context.Context) error {
		_, resetErr := New(postgres.Conn(txCtx, s.pool)).resetAccountPassword(txCtx, resetAccountPasswordParams{
			PasswordHash:    pgconv.TextOrNull(hash),
			Email:           email,
			ExpectedVersion: int64(expectedVersion),
		})
		if resetErr == nil {
			updated = true

			return nil
		}

		if errors.Is(resetErr, pgx.ErrNoRows) {
			return nil
		}

		return fmt.Errorf("resetting account password: %w", adaptererr.Decode(resetErr))
	})
	if err != nil {
		return false, err
	}

	return updated, nil
}

// ByID fetches an account by its UUID.
func (s *AccountStore) ByID(ctx context.Context, id uuid.UUID) (model.Account, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).accountByID(ctx, id)
	if err != nil {
		return model.Account{}, fmt.Errorf("fetching account by id: %w", adaptererr.Decode(err))
	}

	return accountRowToModel(row), nil
}

// ByEmail fetches an account by its verified email.
func (s *AccountStore) ByEmail(ctx context.Context, email string) (model.Account, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).accountByEmail(ctx, email)
	if err != nil {
		return model.Account{}, fmt.Errorf("fetching account by email: %w", adaptererr.Decode(err))
	}

	return accountRowToModel(row), nil
}

// BySubject fetches an account by its external IdP subject.
func (s *AccountStore) BySubject(ctx context.Context, subject string) (model.Account, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).accountBySubject(ctx, pgconv.TextOrNull(subject))
	if err != nil {
		return model.Account{}, fmt.Errorf("fetching account by subject: %w", adaptererr.Decode(err))
	}

	return accountRowToModel(row), nil
}

// accountRowToModel maps a generated Account row to the domain Account.
func accountRowToModel(a Account) model.Account {
	return model.Account{
		ID:          a.ID,
		Subject:     pgconv.StringFromText(a.Subject),
		Email:       a.Email,
		DisplayName: pgconv.StringFromText(a.DisplayName),
		CreatedAt:   a.CreatedAt,
		UpdatedAt:   a.UpdatedAt,
	}
}
