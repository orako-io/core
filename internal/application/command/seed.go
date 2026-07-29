// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/auth"
)

// accountSeedStore is the account write port the seed needs. Satisfied by
// *identity.AccountStore.
type accountSeedStore interface {
	ByEmail(ctx context.Context, email string) (model.Account, error)
	Create(ctx context.Context, account model.Account) error
	SetPassword(ctx context.Context, accountID uuid.UUID, hash string) error
}

// SeedAdmin creates the first local-auth owner (account + org) from
// ORAKO_ADMIN_EMAIL/PASSWORD, so a fresh self-host has someone to log in as.
// Idempotent: a no-op when the env vars are empty or the admin already exists.
// createOrg is a closure over the CreateOrganization use case (keeps this package
// free of the command layer).
func SeedAdmin(
	ctx context.Context,
	accounts accountSeedStore,
	createOrg func(ctx context.Context, orgName string, ownerAccountID uuid.UUID) error,
	email, password string,
) (created bool, err error) {
	if email == "" || password == "" {
		return false, nil // not configured
	}

	if len(password) < minPasswordLen {
		return false, fmt.Errorf("seed admin: ORAKO_ADMIN_PASSWORD must be at least %d characters", minPasswordLen)
	}

	switch _, lookupErr := accounts.ByEmail(ctx, email); {
	case lookupErr == nil:
		return false, nil // already seeded
	case errors.Is(lookupErr, adaptererr.ErrNotFound):
		// fall through and create
	default:
		return false, fmt.Errorf("seed admin: checking existing account: %w", lookupErr)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return false, fmt.Errorf("seed admin: hashing ORAKO_ADMIN_PASSWORD: %w", err)
	}

	id := uuid.New()

	account, err := model.NewAccount(id, email, LocalSubject(id), "")
	if err != nil {
		return false, fmt.Errorf("seed admin: building account: %w", err)
	}

	if err := accounts.Create(ctx, account); err != nil {
		return false, fmt.Errorf("seed admin: creating account: %w", err)
	}

	if err := accounts.SetPassword(ctx, id, hash); err != nil {
		return false, fmt.Errorf("seed admin: setting password: %w", err)
	}

	if err := createOrg(ctx, "My Organization", id); err != nil {
		return false, fmt.Errorf("seed admin: creating organization: %w", err)
	}

	return true, nil
}
