// SPDX-License-Identifier: AGPL-3.0-or-later

package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// PresenceRepository is the write-side port for member connection-presence
// persistence. Driven adapters under internal/adapters implement it; the domain
// owns the contract.
//
// Implementations expose only the sentinel errors from
// internal/adapters/errors, never raw driver errors.
type PresenceRepository interface {
	// Upsert inserts or updates the presence record for the given member.
	// The operation is idempotent: repeated calls with the same memberID are
	// safe and converge to the latest state.
	Upsert(ctx context.Context, presence model.Presence) error
	// ByMember returns the presence record for the given member.
	// Returns adaptererr.ErrNotFound when no record has been written yet.
	ByMember(ctx context.Context, memberID uuid.UUID) (model.Presence, error)
}
