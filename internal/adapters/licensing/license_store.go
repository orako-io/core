// SPDX-License-Identifier: AGPL-3.0-or-later

// Package licensing is the Postgres-backed store for the single self-host
// license key an admin pastes in the dashboard (instance_license). It replaces
// the ORAKO_LICENSE_KEY env var: the key is persisted here and re-resolved at
// runtime, so activating or clearing a license takes effect without a restart.
// The key is still verified OFFLINE (internal/pkg/edition + internal/pkg/license)
// — this package only owns where the key lives, never how it is trusted.
package licensing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/pkg/pgconv"
	postgres "github.com/orako-io/core/internal/pkg/postgres"
)

// LicenseStore is the Postgres-backed store for the single instance license row.
type LicenseStore struct {
	pool *pgxpool.Pool
}

// NewLicenseStore builds a LicenseStore backed by pool.
func NewLicenseStore(pool *pgxpool.Pool) *LicenseStore {
	return &LicenseStore{pool: pool}
}

// Get returns the stored license key plus who set it and when. found is false
// (with a nil error) when no key has been set — the caller treats that miss as
// the Community edition (fail-safe). A read error surfaces so the boot path can
// log a warning and still fall back to Community.
func (s *LicenseStore) Get(ctx context.Context) (key string, setBy uuid.UUID, setAt time.Time, found bool, err error) {
	row, err := New(postgres.Conn(ctx, s.pool)).getInstanceLicense(ctx)
	if err != nil {
		if errors.Is(adaptererr.Decode(err), adaptererr.ErrNotFound) {
			return "", uuid.Nil, time.Time{}, false, nil
		}

		return "", uuid.Nil, time.Time{}, false, fmt.Errorf("fetching instance license: %w", adaptererr.Decode(err))
	}

	return row.LicenseKey, pgconv.UUIDFromPgtype(row.SetBy), row.SetAt, true, nil
}

// Set upserts the single license row to key, recording the org-admin member who
// set it. A nil setBy (uuid.Nil) records no human setter — used by the automatic
// refresh-loop renewal. The caller MUST have verified the key offline first;
// this store never validates a key (verify-before-persist lives in the handler).
func (s *LicenseStore) Set(ctx context.Context, key string, setBy uuid.UUID) error {
	if err := New(postgres.Conn(ctx, s.pool)).upsertInstanceLicense(ctx, upsertInstanceLicenseParams{
		LicenseKey: key,
		SetBy:      pgconv.UUIDOrNull(setBy),
	}); err != nil {
		return fmt.Errorf("storing instance license: %w", adaptererr.Decode(err))
	}

	return nil
}

// Clear removes the stored license (revert to Community). Idempotent: a clear
// with no row is a no-op, not an error.
func (s *LicenseStore) Clear(ctx context.Context) error {
	if err := New(postgres.Conn(ctx, s.pool)).deleteInstanceLicense(ctx); err != nil {
		return fmt.Errorf("clearing instance license: %w", adaptererr.Decode(err))
	}

	return nil
}
