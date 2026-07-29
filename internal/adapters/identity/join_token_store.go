// SPDX-License-Identifier: AGPL-3.0-or-later

package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/pgconv"
	postgres "github.com/orako-io/core/internal/pkg/postgres"
)

// Compile-time assertion that JoinTokenStore satisfies its port.
var _ repository.JoinTokenRepository = (*JoinTokenStore)(nil)

// joinTokenEntropyBytes is the number of crypto/rand bytes behind a join code
// (base64url-encoded → ~32 chars). 24 bytes = 192 bits, far beyond guessing.
const joinTokenEntropyBytes = 24

// JoinTokenStore is the Postgres-backed JoinTokenRepository for shareable org
// join codes.
type JoinTokenStore struct {
	pool *pgxpool.Pool
}

// NewJoinTokenStore builds a JoinTokenStore backed by pool.
func NewJoinTokenStore(pool *pgxpool.Pool) *JoinTokenStore {
	return &JoinTokenStore{pool: pool}
}

// newJoinToken mints a fresh URL-safe join code from crypto/rand.
func newJoinToken() (string, error) {
	raw := make([]byte, joinTokenEntropyBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating join token entropy: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// CreateJoinToken mints a fresh live token for the org, revoking any prior live
// token first. Both writes run in one transaction so the "at most one live code
// per org" partial unique index is never transiently violated.
func (s *JoinTokenStore) CreateJoinToken(ctx context.Context, orgID, projectID, createdByMemberID uuid.UUID) (string, error) {
	token, err := newJoinToken()
	if err != nil {
		return "", err
	}

	params := createJoinTokenParams{
		Token:             token,
		OrgID:             orgID,
		ProjectID:         projectID,
		CreatedByMemberID: pgconv.UUIDOrNull(createdByMemberID),
	}

	if err := postgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)

		if _, revErr := q.revokeLiveOrgJoinTokens(ctx, orgID); revErr != nil {
			return fmt.Errorf("revoking prior live join token: %w", adaptererr.Decode(revErr))
		}

		if createErr := q.createJoinToken(ctx, params); createErr != nil {
			return fmt.Errorf("creating join token: %w", adaptererr.Decode(createErr))
		}

		return nil
	}); err != nil {
		return "", err
	}

	return token, nil
}

// JoinTokenByToken resolves a raw token to its org/project and revoked flag.
// Runs against the ambient transaction when the caller opened one (the redeem
// use case reads inside its provisioning tx).
func (s *JoinTokenStore) JoinTokenByToken(ctx context.Context, token string) (repository.JoinToken, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).joinTokenByToken(ctx, token)
	if err != nil {
		return repository.JoinToken{}, fmt.Errorf("fetching join token: %w", adaptererr.Decode(err))
	}

	return repository.JoinToken{
		Token:     token,
		OrgID:     row.OrgID,
		ProjectID: row.ProjectID,
		Revoked:   row.Revoked,
	}, nil
}

// RevokeJoinToken revokes the org's live token(s). Idempotent: a revoke with no
// live token is a no-op, not an error.
func (s *JoinTokenStore) RevokeJoinToken(ctx context.Context, orgID uuid.UUID) error {
	if _, err := New(postgres.Conn(ctx, s.pool)).revokeLiveOrgJoinTokens(ctx, orgID); err != nil {
		return fmt.Errorf("revoking join token: %w", adaptererr.Decode(err))
	}

	return nil
}

// ActiveJoinToken returns the org's current live token. ok is false (no error)
// when the org has no live token.
func (s *JoinTokenStore) ActiveJoinToken(ctx context.Context, orgID uuid.UUID) (repository.JoinToken, bool, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).activeJoinTokenByOrg(ctx, orgID)
	if err != nil {
		if errors.Is(adaptererr.Decode(err), adaptererr.ErrNotFound) {
			return repository.JoinToken{OrgID: uuid.Nil, ProjectID: uuid.Nil}, false, nil
		}

		return repository.JoinToken{}, false, fmt.Errorf("fetching active join token: %w", adaptererr.Decode(err))
	}

	return repository.JoinToken{
		Token:     row.Token,
		OrgID:     row.OrgID,
		ProjectID: row.ProjectID,
		CreatedAt: row.CreatedAt,
	}, true, nil
}
