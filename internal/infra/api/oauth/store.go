// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/pkg/pgconv"
	pkgpostgres "github.com/orako-io/core/internal/pkg/postgres"
)

// Store persists OAuth clients, authorization codes, and access/refresh
// tokens (raw pgx, not sqlc — mirrors ProjectProviderStore's house style).
// Deliberately placed inside the oauth package rather than the eventlog
// adapter: this is transport-layer OAuth-protocol state, not an application
// domain store.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// projectIDsToText renders a project scope for the text[] column (see the
// 0028 migration for why text[] rather than uuid[]).
func projectIDsToText(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}

	return out
}

// textToProjectIDs parses a stored text[] scope back into UUIDs, preserving
// order (the first is the primary project).
func textToProjectIDs(texts []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(texts))

	for _, t := range texts {
		id, err := uuid.Parse(t)
		if err != nil {
			return nil, fmt.Errorf("oauth: malformed project_id %q in token scope: %w", t, err)
		}

		out = append(out, id)
	}

	return out, nil
}

// CreateClient persists a newly registered client. c.ID must already be set
// (see newClientID).
func (s *Store) CreateClient(ctx context.Context, c Client) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO oauth_clients (client_id, client_name, redirect_uris, grant_types, response_types, token_endpoint_auth_method)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, c.ID, c.Name, c.RedirectURIs, c.GrantTypes, c.ResponseTypes, c.AuthMethod)
	if err != nil {
		return fmt.Errorf("oauth: creating client: %w", adaptererr.Decode(err))
	}

	return nil
}

// CleanupExpired removes protocol artifacts that no longer carry security or
// audit value, then removes old dynamic clients that have never issued a token.
// MachineClientID is permanent and is never considered for collection.
func (s *Store) CleanupExpired(ctx context.Context, unusedClientBefore time.Time) error {
	return pkgpostgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			DELETE FROM oauth_authorization_codes
			WHERE expires_at < NOW() OR used_at < NOW() - INTERVAL '24 hours'
		`); err != nil {
			return fmt.Errorf("oauth: deleting expired authorization codes: %w", adaptererr.Decode(err))
		}

		if _, err := tx.Exec(ctx, `
			DELETE FROM oauth_tokens
			WHERE expires_at < NOW() - INTERVAL '24 hours'
			   OR revoked_at < NOW() - INTERVAL '24 hours'
		`); err != nil {
			return fmt.Errorf("oauth: deleting expired tokens: %w", adaptererr.Decode(err))
		}

		if _, err := tx.Exec(ctx, `
			DELETE FROM oauth_clients c
			WHERE c.client_id <> $1
			  AND c.created_at < $2
			  AND NOT EXISTS (
				SELECT 1 FROM oauth_authorization_codes ac WHERE ac.client_id = c.client_id
			  )
			  AND NOT EXISTS (
				SELECT 1 FROM oauth_tokens ot WHERE ot.client_id = c.client_id
			  )
		`, MachineClientID, unusedClientBefore); err != nil {
			return fmt.Errorf("oauth: deleting unused clients: %w", adaptererr.Decode(err))
		}

		return nil
	})
}

// GetClient resolves a registered client by id. Returns adaptererr.ErrNotFound
// when unknown.
func (s *Store) GetClient(ctx context.Context, clientID string) (Client, error) {
	var c Client

	err := s.pool.QueryRow(ctx, `
		SELECT client_id, client_name, redirect_uris, grant_types, response_types, token_endpoint_auth_method, created_at
		FROM oauth_clients
		WHERE client_id = $1
	`, clientID).Scan(&c.ID, &c.Name, &c.RedirectURIs, &c.GrantTypes, &c.ResponseTypes, &c.AuthMethod, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Client{}, fmt.Errorf("client %s: %w", clientID, adaptererr.ErrNotFound)
		}

		return Client{}, fmt.Errorf("oauth: loading client %s: %w", clientID, err)
	}

	return c, nil
}

// CreateAuthCode persists a freshly issued authorization code, storing only
// its SHA-256 (codeHash).
func (s *Store) CreateAuthCode(ctx context.Context, code AuthCode, codeHash []byte) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO oauth_authorization_codes
			(id, org_id, code_hash, client_id, redirect_uri, code_challenge, code_challenge_method, resource, member_id, expires_at, project_ids)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, code.ID, pgconv.UUIDOrNull(code.OrgID), codeHash, code.ClientID, code.RedirectURI, code.CodeChallenge, code.CodeChallengeMethod,
		code.Resource, code.MemberID, code.ExpiresAt, projectIDsToText(code.ProjectIDs))
	if err != nil {
		return fmt.Errorf("oauth: creating authorization code: %w", adaptererr.Decode(err))
	}

	return nil
}

// ConsumeAuthCode atomically redeems rawCode: it marks the code used and
// returns its bound row in one statement, so a concurrent double-redemption
// race can never succeed twice. Returns adaptererr.ErrNotFound when the code
// is unknown, already used, or expired — the caller collapses all three into
// one invalid_grant response, never distinguishing which (no information to
// leak: an attacker replaying a stolen code learns nothing about why it
// failed).
func (s *Store) ConsumeAuthCode(ctx context.Context, rawCode string) (AuthCode, error) {
	var (
		ac    AuthCode
		scope []string
	)

	err := s.pool.QueryRow(ctx, `
		UPDATE oauth_authorization_codes
		SET used_at = NOW()
		WHERE code_hash = $1 AND used_at IS NULL AND expires_at > NOW()
		RETURNING id, COALESCE(org_id, '`+pgconv.NilOrgID+`'::uuid), client_id, redirect_uri, code_challenge, code_challenge_method, resource, member_id, expires_at, project_ids
	`, HashSecret(rawCode)).Scan(&ac.ID, &ac.OrgID, &ac.ClientID, &ac.RedirectURI, &ac.CodeChallenge, &ac.CodeChallengeMethod,
		&ac.Resource, &ac.MemberID, &ac.ExpiresAt, &scope)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AuthCode{}, fmt.Errorf("authorization code: %w", adaptererr.ErrNotFound)
		}

		return AuthCode{}, fmt.Errorf("oauth: consuming authorization code: %w", err)
	}

	if ac.ProjectIDs, err = textToProjectIDs(scope); err != nil {
		return AuthCode{}, err
	}

	return ac, nil
}

// CreateTokenPair persists a freshly minted access+refresh pair (sharing
// GrantID) in one transaction.
func (s *Store) CreateTokenPair(ctx context.Context, access Token, accessHash []byte, refresh Token, refreshHash []byte) error {
	return pkgpostgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := insertToken(ctx, tx, access, accessHash); err != nil {
			return fmt.Errorf("oauth: creating access token: %w", err)
		}

		if err := insertToken(ctx, tx, refresh, refreshHash); err != nil {
			return fmt.Errorf("oauth: creating refresh token: %w", err)
		}

		return nil
	})
}

// RotateTokenPair atomically claims the presented refresh token and persists
// its replacement access+refresh pair. claimed is false when another request
// already revoked the token; no replacement rows are inserted in that case.
func (s *Store) RotateTokenPair(
	ctx context.Context,
	presentedRefreshID uuid.UUID,
	access Token,
	accessHash []byte,
	refresh Token,
	refreshHash []byte,
) (claimed bool, err error) {
	err = pkgpostgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, revokeErr := tx.Exec(ctx, `
			UPDATE oauth_tokens
			SET revoked_at = NOW()
			WHERE id = $1 AND kind = $2 AND revoked_at IS NULL
		`, presentedRefreshID, string(TokenKindRefresh))
		if revokeErr != nil {
			return fmt.Errorf("oauth: claiming refresh token %s: %w", presentedRefreshID, adaptererr.Decode(revokeErr))
		}

		if tag.RowsAffected() == 0 {
			return nil
		}

		if insertErr := insertToken(ctx, tx, access, accessHash); insertErr != nil {
			return fmt.Errorf("oauth: creating rotated access token: %w", insertErr)
		}

		if insertErr := insertToken(ctx, tx, refresh, refreshHash); insertErr != nil {
			return fmt.Errorf("oauth: creating rotated refresh token: %w", insertErr)
		}

		claimed = true

		return nil
	})
	if err != nil {
		return false, err
	}

	return claimed, nil
}

// insertToken is the shared INSERT behind CreateTokenPair's two rows.
func insertToken(ctx context.Context, tx pgx.Tx, t Token, hash []byte) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO oauth_tokens (id, org_id, member_id, client_id, resource, kind, token_hash, grant_id, expires_at, project_ids)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, t.ID, pgconv.UUIDOrNull(t.OrgID), t.MemberID, t.ClientID, t.Resource, string(t.Kind), hash, t.GrantID, t.ExpiresAt, projectIDsToText(t.ProjectIDs))

	return adaptererr.Decode(err)
}

// GetToken resolves a token row by its raw secret and expected kind,
// regardless of revoked/expired state — the caller (Authenticator or the
// refresh grant handler) decides how to react to each state, since revoked
// vs. expired vs. unknown carry different responses (reuse detection needs to
// tell "revoked" apart from "never existed").
func (s *Store) GetToken(ctx context.Context, rawToken string, kind TokenKind) (Token, error) {
	var (
		t         Token
		revokedAt pgtype.Timestamptz
		lastUsed  pgtype.Timestamptz
		scope     []string
	)

	err := s.pool.QueryRow(ctx, `
		SELECT id, COALESCE(org_id, '`+pgconv.NilOrgID+`'::uuid), member_id, client_id, resource, kind, grant_id, expires_at, revoked_at, last_used_at, project_ids
		FROM oauth_tokens
		WHERE token_hash = $1 AND kind = $2
	`, HashSecret(rawToken), string(kind)).Scan(
		&t.ID, &t.OrgID, &t.MemberID, &t.ClientID, &t.Resource, (*string)(&t.Kind), &t.GrantID, &t.ExpiresAt, &revokedAt, &lastUsed, &scope,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Token{}, fmt.Errorf("token: %w", adaptererr.ErrNotFound)
		}

		return Token{}, fmt.Errorf("oauth: loading token: %w", err)
	}

	if revokedAt.Valid {
		v := revokedAt.Time
		t.RevokedAt = &v
	}

	if lastUsed.Valid {
		v := lastUsed.Time
		t.LastUsedAt = &v
	}

	if t.ProjectIDs, err = textToProjectIDs(scope); err != nil {
		return Token{}, err
	}

	return t, nil
}

// RevokeToken invalidates a single token row. It remains idempotent for
// explicit administrative revocation; refresh rotation uses RotateTokenPair
// instead because claim and replacement must share one transaction.
func (s *Store) RevokeToken(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE oauth_tokens SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL
	`, id)
	if err != nil {
		return fmt.Errorf("oauth: revoking token %s: %w", id, adaptererr.Decode(err))
	}

	return nil
}

// RevokeGrant invalidates every token sharing grantID — the reuse-detection
// response when a rotated-away refresh token is replayed (RFC 6749 §10.4's
// recommended theft response: kill the whole grant, not just the reused
// token), and the mechanism a future "disconnect this agent" dashboard action
// (phase 5) will call.
func (s *Store) RevokeGrant(ctx context.Context, grantID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE oauth_tokens SET revoked_at = NOW() WHERE grant_id = $1 AND revoked_at IS NULL
	`, grantID)
	if err != nil {
		return fmt.Errorf("oauth: revoking grant %s: %w", grantID, adaptererr.Decode(err))
	}

	return nil
}

// ListGrantsByMember returns one row per grant in an org for memberID,
// aggregating the access+refresh pair (and every pair produced by rotating
// it) sharing that grant — the dashboard's Connections list. Only live grants
// are returned: a grant with every token revoked (the caller already
// disconnected it, or reuse detection killed it) is omitted entirely rather
// than shown as a dead row. Newest connection first.
func (s *Store) ListGrantsByMember(ctx context.Context, orgID, memberID uuid.UUID) ([]Grant, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ot.grant_id, oc.client_name, ot.resource, ot.project_ids,
		       MIN(ot.created_at) AS connected_at,
		       MAX(ot.last_used_at) AS last_used_at,
		       MAX(ot.expires_at) AS expires_at
		FROM oauth_tokens ot
		JOIN oauth_clients oc ON oc.client_id = ot.client_id
		WHERE ot.org_id = $1 AND ot.member_id = $2
		GROUP BY ot.grant_id, oc.client_name, ot.resource, ot.project_ids
		HAVING bool_or(ot.revoked_at IS NULL)
		ORDER BY connected_at DESC
	`, orgID, memberID)
	if err != nil {
		return nil, fmt.Errorf("oauth: listing grants for member %s: %w", memberID, adaptererr.Decode(err))
	}
	defer rows.Close()

	var grants []Grant

	for rows.Next() {
		var (
			g        Grant
			scope    []string
			lastUsed pgtype.Timestamptz
		)

		if err := rows.Scan(&g.GrantID, &g.ClientName, &g.Resource, &scope, &g.ConnectedAt, &lastUsed, &g.ExpiresAt); err != nil {
			return nil, fmt.Errorf("oauth: scanning grant row: %w", err)
		}

		if lastUsed.Valid {
			v := lastUsed.Time
			g.LastUsedAt = &v
		}

		if g.ProjectIDs, err = textToProjectIDs(scope); err != nil {
			return nil, err
		}

		grants = append(grants, g)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("oauth: reading grant rows: %w", err)
	}

	return grants, nil
}

// RevokeGrantForMember invalidates every token sharing grantID, scoped to the
// active org and member so a caller can never revoke another grant — the
// dashboard's "disconnect this agent" action. Returns adaptererr.ErrNotFound
// when the grant does not belong to that org/member or is already
// fully revoked.
func (s *Store) RevokeGrantForMember(ctx context.Context, orgID, memberID, grantID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE oauth_tokens SET revoked_at = NOW()
		WHERE grant_id = $1 AND org_id = $2 AND member_id = $3 AND revoked_at IS NULL
	`, grantID, orgID, memberID)
	if err != nil {
		return fmt.Errorf("oauth: revoking grant %s for member %s: %w", grantID, memberID, adaptererr.Decode(err))
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("grant %s: %w", grantID, adaptererr.ErrNotFound)
	}

	return nil
}

// TouchToken stamps last_used_at; called on successful resource-server
// authentication.
func (s *Store) TouchToken(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE oauth_tokens SET last_used_at = NOW() WHERE id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("oauth: touching token %s: %w", id, adaptererr.Decode(err))
	}

	return nil
}

// CreateMachineToken persists a freshly minted machine token (phase 1): a
// single row, unlike CreateTokenPair's two — a machine token has no paired
// refresh row. Returns the database-assigned created_at (never set on t by
// the caller, exactly like insertToken's rows).
func (s *Store) CreateMachineToken(ctx context.Context, t Token, hash []byte, label string) (time.Time, error) {
	var createdAt time.Time

	err := s.pool.QueryRow(ctx, `
		INSERT INTO oauth_tokens (id, org_id, member_id, client_id, resource, kind, token_hash, grant_id, expires_at, project_ids, label)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING created_at
	`, t.ID, pgconv.UUIDOrNull(t.OrgID), t.MemberID, t.ClientID, t.Resource, string(t.Kind), hash, t.GrantID, t.ExpiresAt, projectIDsToText(t.ProjectIDs), label).Scan(&createdAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("oauth: creating machine token: %w", adaptererr.Decode(err))
	}

	return createdAt, nil
}

// ListMachineTokens returns orgID's minted machine tokens (live and revoked,
// so the dashboard can show history), newest first — every org admin sees
// every machine token in the org, not just the ones they personally minted.
func (s *Store) ListMachineTokens(ctx context.Context, orgID uuid.UUID) ([]MachineToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ot.id, ot.label, ot.project_ids, ot.created_at, ot.expires_at, ot.last_used_at, ot.revoked_at
		FROM oauth_tokens ot
		WHERE ot.client_id = $1
		  AND ot.org_id = $2
		ORDER BY ot.created_at DESC
	`, MachineClientID, orgID)
	if err != nil {
		return nil, fmt.Errorf("oauth: listing machine tokens for org %s: %w", orgID, adaptererr.Decode(err))
	}
	defer rows.Close()

	var tokens []MachineToken

	for rows.Next() {
		var (
			mt        MachineToken
			label     pgtype.Text
			scope     []string
			lastUsed  pgtype.Timestamptz
			revokedAt pgtype.Timestamptz
		)

		if err := rows.Scan(&mt.ID, &label, &scope, &mt.CreatedAt, &mt.ExpiresAt, &lastUsed, &revokedAt); err != nil {
			return nil, fmt.Errorf("oauth: scanning machine token row: %w", err)
		}

		mt.Label = label.String

		if mt.ProjectIDs, err = textToProjectIDs(scope); err != nil {
			return nil, err
		}

		if lastUsed.Valid {
			v := lastUsed.Time
			mt.LastUsedAt = &v
		}

		if revokedAt.Valid {
			v := revokedAt.Time
			mt.RevokedAt = &v
		}

		tokens = append(tokens, mt)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("oauth: reading machine token rows: %w", err)
	}

	return tokens, nil
}

// RevokeMachineToken invalidates one of orgID's machine tokens by id — ANY
// admin of the org that owns the token, not only the admin who minted it
// (see ListMachineTokens for why). Scoped to both orgID, via the same
// EXISTS(project_members ⋈ projects) org-membership check, so an org-A admin
// can never revoke an org-B token even knowing its id — and MachineClientID
// (never an OAuth-flow connection sharing this table). Returns
// adaptererr.ErrNotFound when the token does not exist, does not belong to
// orgID, is not a machine token, or is already revoked.
func (s *Store) RevokeMachineToken(ctx context.Context, orgID, tokenID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE oauth_tokens ot SET revoked_at = NOW()
		WHERE ot.id = $1 AND ot.client_id = $2 AND ot.revoked_at IS NULL
		  AND ot.org_id = $3
	`, tokenID, MachineClientID, orgID)
	if err != nil {
		return fmt.Errorf("oauth: revoking machine token %s for org %s: %w", tokenID, orgID, adaptererr.Decode(err))
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("machine token %s: %w", tokenID, adaptererr.ErrNotFound)
	}

	return nil
}
