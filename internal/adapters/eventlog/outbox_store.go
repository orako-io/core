// SPDX-License-Identifier: AGPL-3.0-or-later

package eventlog

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/messaging"
	postgres "github.com/orako-io/core/internal/pkg/postgres"
)

// OutboxStore is the Postgres-backed port the outbox relay reads through: the
// durable log above a watermark, plus the single relay watermark it advances.
type OutboxStore struct {
	pool *pgxpool.Pool
}

// NewOutboxStore builds an OutboxStore backed by pool.
func NewOutboxStore(pool *pgxpool.Pool) *OutboxStore {
	return &OutboxStore{pool: pool}
}

// SeedWatermark pins subscriber's watermark to the current head of the log, but
// only if it has none yet (ON CONFLICT DO NOTHING) — so a first-ever boot never
// replays the whole history.
func (s *OutboxStore) SeedWatermark(ctx context.Context, subscriber string) error {
	if err := New(postgres.Conn(ctx, s.pool)).seedProjectorOffset(ctx, subscriber); err != nil {
		return fmt.Errorf("seeding projector offset: %w", adaptererr.Decode(err))
	}

	return nil
}

// Watermark returns subscriber's last delivered global_seq, or 0 if it has none.
func (s *OutboxStore) Watermark(ctx context.Context, subscriber string) (int64, error) {
	seq, err := New(postgres.Conn(ctx, s.pool)).getProjectorOffset(ctx, subscriber)
	if err != nil {
		if errors.Is(adaptererr.Decode(err), adaptererr.ErrNotFound) {
			return 0, nil
		}

		return 0, fmt.Errorf("reading projector offset: %w", adaptererr.Decode(err))
	}

	return seq, nil
}

// SetWatermark records subscriber's last delivered global_seq (used by the relay,
// which advances strictly in order).
func (s *OutboxStore) SetWatermark(ctx context.Context, subscriber string, globalSeq int64) error {
	if err := New(postgres.Conn(ctx, s.pool)).upsertProjectorOffset(ctx, upsertProjectorOffsetParams{
		Subscriber:    subscriber,
		LastGlobalSeq: globalSeq,
	}); err != nil {
		return fmt.Errorf("upserting projector offset: %w", adaptererr.Decode(err))
	}

	return nil
}

// AdvanceWatermark moves subscriber's watermark from fromSeq to toSeq only when
// it currently sits at fromSeq (contiguous). ok is false when the position did
// not match — a concurrent publish moved it, or a gap the relay must fill. Used
// by the publish hot path.
func (s *OutboxStore) AdvanceWatermark(ctx context.Context, subscriber string, fromSeq, toSeq int64) (bool, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).advanceProjectorOffset(ctx, advanceProjectorOffsetParams{
		Subscriber: subscriber,
		FromSeq:    fromSeq,
		ToSeq:      toSeq,
	})
	if err != nil {
		return false, fmt.Errorf("advancing projector offset: %w", adaptererr.Decode(err))
	}

	return rows > 0, nil
}

// EventsAfter returns up to limit events with global_seq greater than after,
// ordered ascending, as relay rows carrying the global position and the stored
// protojson payload.
func (s *OutboxStore) EventsAfter(ctx context.Context, after int64, limit int32) ([]messaging.OutboxRow, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).eventsAfterGlobalSeq(ctx, eventsAfterGlobalSeqParams{
		GlobalSeq: pgtypeInt8(after),
		Limit:     limit,
	})
	if err != nil {
		return nil, fmt.Errorf("reading events after %d: %w", after, adaptererr.Decode(err))
	}

	out := make([]messaging.OutboxRow, len(rows))
	for i, r := range rows {
		out[i] = messaging.OutboxRow{
			GlobalSeq: r.GlobalSeq.Int64,
			Payload:   r.Payload,
		}
	}

	return out, nil
}

// pgtypeInt8 wraps a plain int64 as a valid pgtype.Int8 for a query parameter.
func pgtypeInt8(v int64) pgtype.Int8 {
	return pgtype.Int8{Int64: v, Valid: true}
}
