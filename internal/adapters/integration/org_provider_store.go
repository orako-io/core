// SPDX-License-Identifier: AGPL-3.0-or-later

package integration

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/pkg/secretbox"
)

// OrgProviderRow is one (org, kind) provider row with its raw credential JSON.
type OrgProviderRow struct {
	OrgID       uuid.UUID
	Kind        string
	Credentials []byte
}

// OrgProviderStore persists messaging provider credentials keyed by
// organization in the org_providers table (raw pgx, not sqlc). The connection
// is org-level because Orako DMs people (org-level identities) and one
// workspace serves all the org's projects; the per-project alert channel stays
// in ProjectProviderStore.
type OrgProviderStore struct {
	pool   *pgxpool.Pool
	cipher *secretbox.Cipher
}

// NewOrgProviderStore builds an OrgProviderStore backed by pool. cipher encrypts
// credentials at rest (A3); a nil cipher stores them as plaintext (legacy).
func NewOrgProviderStore(pool *pgxpool.Pool, cipher *secretbox.Cipher) *OrgProviderStore {
	return &OrgProviderStore{pool: pool, cipher: cipher}
}

// UpsertProvider inserts or replaces the credentials for (orgID, kind).
func (s *OrgProviderStore) UpsertProvider(ctx context.Context, orgID uuid.UUID, kind string, credentials []byte) error {
	sealed, err := s.cipher.Encrypt(credentials)
	if err != nil {
		return fmt.Errorf("org_provider_store: encrypting %s credentials for org %s: %w", kind, orgID, err)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO org_providers (id, org_id, kind, credentials)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (org_id, kind) DO UPDATE
		    SET credentials = EXCLUDED.credentials,
		        updated_at  = NOW()
	`, uuid.New(), orgID, kind, sealed)
	if err != nil {
		return fmt.Errorf("org_provider_store: upserting %s credentials for org %s: %w", kind, orgID, adaptererr.Decode(err))
	}

	return nil
}

// LoadProvider returns the raw credential JSON for the exact (orgID, kind)
// pair. Returns adaptererr.ErrNotFound when absent.
func (s *OrgProviderStore) LoadProvider(ctx context.Context, orgID uuid.UUID, kind string) (credentials []byte, err error) {
	return loadCredential(ctx, s.pool, s.cipher,
		`SELECT credentials FROM org_providers WHERE org_id = $1 AND kind = $2`,
		fmt.Errorf("org %s kind %s: %w", orgID, kind, adaptererr.ErrNotFound),
		orgID, kind,
	)
}

// DeleteProvider removes the (orgID, kind) row. Idempotent.
func (s *OrgProviderStore) DeleteProvider(ctx context.Context, orgID uuid.UUID, kind string) error {
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM org_providers WHERE org_id = $1 AND kind = $2
	`, orgID, kind); err != nil {
		return fmt.Errorf("org_provider_store: deleting %s provider for org %s: %w", kind, orgID, adaptererr.Decode(err))
	}

	return nil
}

// ConfiguredKinds returns the provider kinds configured for an org (empty slice
// when none).
func (s *OrgProviderStore) ConfiguredKinds(ctx context.Context, orgID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT kind FROM org_providers WHERE org_id = $1 ORDER BY kind`, orgID)
	if err != nil {
		return nil, fmt.Errorf("org_provider_store: listing kinds for org %s: %w", orgID, adaptererr.Decode(err))
	}
	defer rows.Close()

	kinds := []string{}

	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			return nil, fmt.Errorf("org_provider_store: scanning kind: %w", err)
		}

		kinds = append(kinds, kind)
	}

	return kinds, rows.Err()
}

// LoadAllProviders returns every configured (org, kind) pair, to hydrate the
// registry at startup.
func (s *OrgProviderStore) LoadAllProviders(ctx context.Context) ([]OrgProviderRow, error) {
	rows, err := s.pool.Query(ctx, `SELECT org_id, kind, credentials FROM org_providers`)
	if err != nil {
		return nil, fmt.Errorf("org_provider_store: loading all providers: %w", adaptererr.Decode(err))
	}
	defer rows.Close()

	out := []OrgProviderRow{}

	for rows.Next() {
		var row OrgProviderRow
		if err := rows.Scan(&row.OrgID, &row.Kind, &row.Credentials); err != nil {
			return nil, fmt.Errorf("org_provider_store: scanning provider row: %w", err)
		}

		row.Credentials, err = s.cipher.Decrypt(row.Credentials)
		if err != nil {
			return nil, fmt.Errorf("org_provider_store: decrypting %s credentials for org %s: %w", row.Kind, row.OrgID, err)
		}

		out = append(out, row)
	}

	return out, rows.Err()
}
