// SPDX-License-Identifier: AGPL-3.0-or-later

package eventlog

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
	postgres "github.com/orako-io/core/internal/pkg/postgres"
)

// Compile-time assertion that Store satisfies the EventStore port.
var _ service.EventStore = (*Store)(nil)

// Store is the Postgres-backed EventStore. It persists events to the
// event_log table at the next per-project monotonic sequence position, using
// the (project_id, seq) unique constraint to surface concurrent conflicts
// rather than silently duplicate.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Append durably inserts ev at the next per-project seq. A transaction-level
// advisory lock keyed by the project UUID serializes concurrent appends to
// the same project (different projects hash to different locks and never block
// each other). MAX+1 is computed and INSERTed inside the same transaction so
// the sequence is always gapless and no retry is needed.
// The input ev.Seq is ignored; the returned event carries the assigned seq.
func (s *Store) Append(ctx context.Context, ev model.Event) (model.Event, error) {
	var result model.Event

	err := postgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error

		result, err = appendEventInTx(ctx, tx, ev)

		return err
	})
	if err != nil {
		return model.Event{}, fmt.Errorf("appending event: %w", err)
	}

	return result, nil
}

func appendEventInTx(ctx context.Context, tx pgx.Tx, ev model.Event) (model.Event, error) {
	// Serialize concurrent appends to the same project with a transaction-level
	// advisory lock. Different projects hash to different keys.
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`,
		ev.ProjectID.String(),
	); err != nil {
		return model.Event{}, adaptererr.Decode(err)
	}

	row, err := New(tx).appendEvent(ctx, appendEventParams{
		ID:         ev.ID,
		ProjectID:  ev.ProjectID,
		Type:       ev.Type.String(),
		Payload:    ev.Payload,
		OccurredAt: ev.OccurredAt,
	})
	if err != nil {
		return model.Event{}, adaptererr.Decode(err)
	}

	return eventLogToModel(row), nil
}

// Replay returns all events for projectID with Seq > sinceSeq, ordered by Seq
// ascending. Pass sinceSeq = 0 to replay the entire project log.
func (s *Store) Replay(ctx context.Context, projectID uuid.UUID, sinceSeq int64) ([]model.Event, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).eventsByProjectSince(ctx, eventsByProjectSinceParams{
		ProjectID: projectID,
		Seq:       sinceSeq,
	})
	if err != nil {
		return nil, fmt.Errorf("replaying events: %w", adaptererr.Decode(err))
	}

	events := make([]model.Event, len(rows))
	for i, r := range rows {
		events[i] = eventLogToModel(r)
	}

	return events, nil
}

// eventLogToModel maps a sqlc EventLog row to a domain Event.
func eventLogToModel(r EventLog) model.Event {
	return model.Event{
		ID:         r.ID,
		ProjectID:  r.ProjectID,
		Seq:        r.Seq,
		GlobalSeq:  r.GlobalSeq.Int64, // BIGSERIAL: NOT NULL in practice, always valid
		Type:       eventTypeFromString(r.Type),
		Payload:    r.Payload,
		OccurredAt: r.OccurredAt,
	}
}

// eventTypeFromString parses the DB text column back into a domain EventType.
func eventTypeFromString(s string) model.EventType {
	switch s {
	case "conversation_opened":
		return model.EventTypeConversationOpened
	case "message_posted":
		return model.EventTypeMessagePosted
	case "conversation_closed":
		return model.EventTypeConversationClosed
	case "kb_entry_created":
		return model.EventTypeKBEntryCreated
	case "kb_answer_superseded":
		return model.EventTypeKBAnswerSuperseded
	case "kb_feedback":
		return model.EventTypeKBFeedback
	case "member_lifecycle":
		return model.EventTypeMemberLifecycle
	case "presence_changed":
		return model.EventTypePresenceChanged
	case "provider_configured":
		return model.EventTypeProviderConfigured
	default:
		return model.EventTypeUnspecified
	}
}
