// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/identity"
	"github.com/orako-io/core/internal/pkg/postgres"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

func TestSeedAdminRollsBackAccountWhenOrganizationFails(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	accounts := identity.NewAccountStore(pool)
	email := uuid.NewString() + "@example.test"

	_, err := SeedAdmin(
		t.Context(),
		accounts,
		postgres.NewTransactor(pool),
		func(context.Context, string, uuid.UUID) error {
			return errors.New("organization write failed")
		},
		email,
		"password123",
	)
	if err == nil {
		t.Fatal("SeedAdmin: want error, got nil")
	}

	if _, err := accounts.ByEmail(t.Context(), email); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("account survived rollback: got %v, want ErrNotFound", err)
	}
}
