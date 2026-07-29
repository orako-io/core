// SPDX-License-Identifier: AGPL-3.0-or-later

package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// AccountRepository is the write/read port for authenticated-account identity.
// Driven adapters under internal/adapters implement it; the domain owns the
// contract. Implementations expose only the sentinel errors from
// internal/adapters/errors.
type AccountRepository interface {
	// Create durably stores a new account. Returns adaptererr.ErrDuplicate when
	// the email or subject already exists.
	Create(ctx context.Context, account model.Account) error
	// ByID fetches an account by its UUID. Returns adaptererr.ErrNotFound when absent.
	ByID(ctx context.Context, id uuid.UUID) (model.Account, error)
	// ByEmail fetches an account by its verified email. Returns
	// adaptererr.ErrNotFound when absent.
	ByEmail(ctx context.Context, email string) (model.Account, error)
	// BySubject fetches an account by its external IdP subject. Returns
	// adaptererr.ErrNotFound when absent.
	BySubject(ctx context.Context, subject string) (model.Account, error)
}
