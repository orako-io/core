// SPDX-License-Identifier: AGPL-3.0-or-later

package presence

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/postgres"
)

// Compile-time assertion that Store satisfies the PresenceRepository port.
var _ repository.PresenceRepository = (*Store)(nil)

// Store is the Postgres-backed PresenceRepository. It persists the
// most recent connection-presence state for each member.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Upsert inserts or updates the presence record for presence.MemberID.
// The operation is idempotent; repeated calls converge to the latest state.
func (s *Store) Upsert(ctx context.Context, presence model.Presence) error {
	_, err := New(postgres.Conn(ctx, s.pool)).upsertPresence(ctx, upsertPresenceParams{
		MemberID: presence.MemberID,
		Online:   presence.Online,
	})
	if err != nil {
		return fmt.Errorf("upserting presence: %w", adaptererr.Decode(err))
	}

	return nil
}

// ByMember returns the presence record for memberID.
// Returns adaptererr.ErrNotFound when no record has been written yet.
func (s *Store) ByMember(ctx context.Context, memberID uuid.UUID) (model.Presence, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).presenceByMember(ctx, memberID)
	if err != nil {
		return model.Presence{}, fmt.Errorf("fetching presence: %w", adaptererr.Decode(err))
	}

	return model.Presence{
		MemberID:  row.MemberID,
		Online:    row.Online,
		UpdatedAt: row.UpdatedAt,
	}, nil
}

// ReadOnline reports whether the member is online now, resolving the freshness
// decay in the adapter so the read side never touches the domain aggregate.
// Returns adaptererr.ErrNotFound when no presence record exists yet.
func (s *Store) ReadOnline(ctx context.Context, memberID uuid.UUID) (bool, error) {
	presence, err := s.ByMember(ctx, memberID)
	if err != nil {
		return false, err
	}

	return presence.OnlineNow(), nil
}

// ReadOnlineByMembers returns the fresh online state for the supplied members.
// Members without a presence row are omitted and therefore offline.
func (s *Store) ReadOnlineByMembers(ctx context.Context, memberIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	if len(memberIDs) == 0 {
		return map[uuid.UUID]bool{}, nil
	}

	rows, err := New(postgres.Conn(ctx, s.pool)).presenceByMembers(ctx, memberIDs)
	if err != nil {
		return nil, fmt.Errorf("fetching presence by members: %w", adaptererr.Decode(err))
	}

	online := make(map[uuid.UUID]bool, len(rows))
	for _, row := range rows {
		presence := model.Presence{
			MemberID:  row.MemberID,
			Online:    row.Online,
			UpdatedAt: row.UpdatedAt,
		}
		online[row.MemberID] = presence.OnlineNow()
	}

	return online, nil
}
