// SPDX-License-Identifier: AGPL-3.0-or-later

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/service"
	postgres "github.com/orako-io/core/internal/pkg/postgres"
	"github.com/orako-io/core/internal/pkg/secretbox"
)

// SlackCredentials holds the per-project Slack OAuth token and team info as
// stored in the project_providers table.
//
// The credentials column is encrypted at rest by the store when
// ORAKO_ENCRYPTION_KEY is set (secretbox / A3); otherwise it is plaintext JSONB.
type SlackCredentials struct {
	BotToken string `json:"bot_token"`
	TeamID   string `json:"team_id"`
	TeamName string `json:"team_name"`
	AppID    string `json:"app_id"`
}

// ProjectProviderRow is a type alias kept for backward compatibility.
//
// Deprecated: use service.ProviderRow.
type ProjectProviderRow = service.ProviderRow

// ProjectProviderStore persists per-project messaging provider credentials in
// the project_providers table (raw pgx, not sqlc).
type ProjectProviderStore struct {
	pool   *pgxpool.Pool
	cipher *secretbox.Cipher
}

// NewProjectProviderStore builds a ProjectProviderStore backed by pool. cipher
// encrypts credentials at rest (A3); a nil cipher stores them as plaintext.
func NewProjectProviderStore(pool *pgxpool.Pool, cipher *secretbox.Cipher) *ProjectProviderStore {
	return &ProjectProviderStore{pool: pool, cipher: cipher}
}

// UpsertProvider inserts or updates the provider credentials (raw JSON) and
// alert channel for a project and kind. alertChannelID follows the
// empty-means-unchanged convention used elsewhere (e.g.
// updateOrgEscalationSettings): on a fresh insert, empty simply stores empty
// (falls back to the org default at read time); on a conflict (credential
// rotation), an empty alertChannelID leaves the previously stored value
// alone rather than clobbering it — ConfigureProvider always resends the
// full credential map on reconnect, so without this, rotating a bot token
// with the alert-channel field left blank would silently wipe a configured
// alert channel. There is currently no sentinel to explicitly clear it via
// this path (unlike default_alert_channel_id's "-"); clearing requires a
// direct store call.
func (s *ProjectProviderStore) UpsertProvider(ctx context.Context, projectID uuid.UUID, kind string, credentials []byte, alertChannelIDs []string) error {
	if alertChannelIDs == nil {
		alertChannelIDs = []string{}
	}

	sealed, err := s.cipher.Encrypt(credentials)
	if err != nil {
		return fmt.Errorf("provider_store: encrypting %s credentials for %s: %w", kind, projectID, err)
	}

	return postgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO project_providers (id, project_id, kind, credentials, alert_channel_ids)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (project_id, kind) DO UPDATE
			    SET credentials       = EXCLUDED.credentials,
			        alert_channel_ids = CASE
			            WHEN cardinality(EXCLUDED.alert_channel_ids) = 0 THEN project_providers.alert_channel_ids
			            ELSE EXCLUDED.alert_channel_ids
			        END,
			        updated_at        = NOW()
		`, uuid.New(), projectID, kind, sealed, alertChannelIDs)
		if err != nil {
			return fmt.Errorf("provider_store: upserting %s credentials for %s: %w", kind, projectID, adaptererr.Decode(err))
		}

		return nil
	})
}

// UpdateAlertChannels replaces the alert channels for an existing
// (projectID, kind) provider WITHOUT touching credentials — the edit-channels
// path when the caller has no secrets to resend. Returns adaptererr.ErrNotFound
// when the provider is not configured. alertChannelIDs may be empty (clears).
func (s *ProjectProviderStore) UpdateAlertChannels(ctx context.Context, projectID uuid.UUID, kind string, alertChannelIDs []string) error {
	if alertChannelIDs == nil {
		alertChannelIDs = []string{}
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE project_providers
		SET alert_channel_ids = $3, updated_at = NOW()
		WHERE project_id = $1 AND kind = $2
	`, projectID, kind, alertChannelIDs)
	if err != nil {
		return fmt.Errorf("provider_store: updating alert channels for %s/%s: %w", projectID, kind, adaptererr.Decode(err))
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("project %s kind %s: %w", projectID, kind, adaptererr.ErrNotFound)
	}

	return nil
}

// UpsertAlertChannels inserts or updates the per-project alert-channel override
// for a kind WITHOUT credentials — the org owns the connection (org_providers),
// this row is the project's optional alert-channel override. Credentials stay
// NULL (allowed since migration 0024). alertChannelIDs may be empty.
func (s *ProjectProviderStore) UpsertAlertChannels(ctx context.Context, projectID uuid.UUID, kind string, alertChannelIDs []string) error {
	if alertChannelIDs == nil {
		alertChannelIDs = []string{}
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO project_providers (id, project_id, kind, alert_channel_ids)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, kind) DO UPDATE
		    SET alert_channel_ids = EXCLUDED.alert_channel_ids, updated_at = NOW()
	`, uuid.New(), projectID, kind, alertChannelIDs)
	if err != nil {
		return fmt.Errorf("provider_store: upserting alert channels for %s/%s: %w", projectID, kind, adaptererr.Decode(err))
	}

	return nil
}

// DeleteProvider removes the (projectID, kind) provider row. Idempotent:
// deleting an absent provider returns nil (DisconnectProvider is idempotent).
func (s *ProjectProviderStore) DeleteProvider(ctx context.Context, projectID uuid.UUID, kind string) error {
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM project_providers
		WHERE project_id = $1 AND kind = $2
	`, projectID, kind); err != nil {
		return fmt.Errorf("provider_store: deleting %s provider for %s: %w", kind, projectID, adaptererr.Decode(err))
	}

	return nil
}

// LoadProvider fetches the raw credential JSON for the exact (projectID,
// kind) pair. Returns adaptererr.ErrNotFound when that pair has no stored
// row — even when the project has other kinds configured; a caller that
// already knows which kind it needs (the registry's per-kind read-through,
// the Teams webhook's bot_app_id lookup) must never be handed a different
// kind's credentials picked by recency.
func (s *ProjectProviderStore) LoadProvider(ctx context.Context, projectID uuid.UUID, kind string) (credentials []byte, err error) {
	return loadCredential(ctx, s.pool, s.cipher,
		`SELECT credentials FROM project_providers WHERE project_id = $1 AND kind = $2`,
		fmt.Errorf("project %s kind %s: %w", projectID, kind, adaptererr.ErrNotFound),
		projectID, kind,
	)
}

// loadCredential runs a single-row credentials SELECT and decrypts the result,
// shared by the org- and project-level provider stores. notFound is returned
// (already wrapped by the caller) when the row is absent.
func loadCredential(ctx context.Context, pool *pgxpool.Pool, cipher *secretbox.Cipher, query string, notFound error, args ...any) ([]byte, error) {
	var sealed []byte

	if err := pool.QueryRow(ctx, query, args...).Scan(&sealed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFound
		}

		return nil, fmt.Errorf("loading provider credentials: %w", err)
	}

	credentials, err := cipher.Decrypt(sealed)
	if err != nil {
		return nil, fmt.Errorf("decrypting provider credentials: %w", err)
	}

	return credentials, nil
}

// ConfiguredKinds returns the provider kinds configured for a project — an
// empty slice (not an error) when none. Reads only the kind column, never the
// credentials.
func (s *ProjectProviderStore) ConfiguredKinds(ctx context.Context, projectID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT kind
		FROM project_providers
		WHERE project_id = $1
		ORDER BY kind
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("provider_store: listing configured kinds for %s: %w", projectID, err)
	}
	defer rows.Close()

	kinds := []string{}

	for rows.Next() {
		var kind string

		if err := rows.Scan(&kind); err != nil {
			return nil, fmt.Errorf("provider_store: scanning kind row: %w", err)
		}

		kinds = append(kinds, kind)
	}

	return kinds, rows.Err()
}

// ConfiguredProvidersWithAlertChannel returns each configured provider kind
// for projectID with its stored alert_channel_id (empty when none is set).
// Implements service.ProviderAlertChannelsReader — the read-side
// counterpart to UpsertProvider's alertChannelID parameter, so the alert
// channel can be displayed/re-edited without resending credentials.
func (s *ProjectProviderStore) ConfiguredProvidersWithAlertChannel(ctx context.Context, projectID uuid.UUID) ([]service.ProviderAlertChannel, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT kind, alert_channel_ids
		FROM project_providers
		WHERE project_id = $1
		ORDER BY kind
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("provider_store: listing configured providers with alert channel for %s: %w", projectID, err)
	}
	defer rows.Close()

	result := []service.ProviderAlertChannel{}

	for rows.Next() {
		var row service.ProviderAlertChannel

		if err := rows.Scan(&row.Kind, &row.AlertChannelIDs); err != nil {
			return nil, fmt.Errorf("provider_store: scanning provider alert channel row: %w", err)
		}

		result = append(result, row)
	}

	return result, rows.Err()
}

// LoadAllProviders returns all configured project providers; used at startup
// to hydrate the provider registry. Implements service.AllProvidersLoader.
func (s *ProjectProviderStore) LoadAllProviders(ctx context.Context) ([]service.ProviderRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT project_id, kind, credentials
		FROM project_providers
		ORDER BY project_id, kind
	`)
	if err != nil {
		return nil, fmt.Errorf("provider_store: listing all providers: %w", err)
	}
	defer rows.Close()

	var result []service.ProviderRow

	for rows.Next() {
		var row service.ProviderRow

		if err := rows.Scan(&row.ProjectID, &row.Kind, &row.Credentials); err != nil {
			return nil, fmt.Errorf("provider_store: scanning provider row: %w", err)
		}

		row.Credentials, err = s.cipher.Decrypt(row.Credentials)
		if err != nil {
			return nil, fmt.Errorf("provider_store: decrypting %s credentials for %s: %w", row.Kind, row.ProjectID, err)
		}

		result = append(result, row)
	}

	return result, rows.Err()
}

// UpsertSlack inserts or updates the Slack credentials for a project
// (backward-compatible wrapper over UpsertProvider; no alert channel).
func (s *ProjectProviderStore) UpsertSlack(ctx context.Context, projectID uuid.UUID, creds SlackCredentials) error {
	data, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("provider_store: marshaling slack credentials: %w", err)
	}

	return s.UpsertProvider(ctx, projectID, "slack", data, nil)
}

// LoadSlack fetches the Slack credentials for a project.
// Returns adaptererr.ErrNotFound when none are stored.
func (s *ProjectProviderStore) LoadSlack(ctx context.Context, projectID uuid.UUID) (SlackCredentials, error) {
	var sealed []byte

	err := s.pool.QueryRow(ctx, `
		SELECT credentials
		FROM project_providers
		WHERE project_id = $1 AND kind = 'slack'
	`, projectID).Scan(&sealed)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SlackCredentials{}, fmt.Errorf("project %s: %w", projectID, adaptererr.ErrNotFound)
		}

		return SlackCredentials{}, fmt.Errorf("provider_store: loading slack credentials: %w", err)
	}

	raw, err := s.cipher.Decrypt(sealed)
	if err != nil {
		return SlackCredentials{}, fmt.Errorf("provider_store: decrypting slack credentials: %w", err)
	}

	var creds SlackCredentials
	if err := json.Unmarshal(raw, &creds); err != nil {
		return SlackCredentials{}, fmt.Errorf("provider_store: decoding slack credentials: %w", err)
	}

	return creds, nil
}

// LoadAllSlack returns all projects that have Slack credentials stored.
//
// Deprecated: prefer LoadAllProviders for kind-agnostic startup hydration.
func (s *ProjectProviderStore) LoadAllSlack(ctx context.Context) (map[uuid.UUID]SlackCredentials, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT project_id, credentials
		FROM project_providers
		WHERE kind = 'slack'
	`)
	if err != nil {
		return nil, fmt.Errorf("provider_store: listing slack credentials: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID]SlackCredentials)

	for rows.Next() {
		var projectID uuid.UUID

		var sealed []byte

		if err := rows.Scan(&projectID, &sealed); err != nil {
			return nil, fmt.Errorf("provider_store: scanning slack row: %w", err)
		}

		raw, err := s.cipher.Decrypt(sealed)
		if err != nil {
			return nil, fmt.Errorf("provider_store: decrypting credentials for %s: %w", projectID, err)
		}

		var creds SlackCredentials
		if err := json.Unmarshal(raw, &creds); err != nil {
			return nil, fmt.Errorf("provider_store: decoding credentials for %s: %w", projectID, err)
		}

		result[projectID] = creds
	}

	return result, rows.Err()
}
