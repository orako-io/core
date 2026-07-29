// SPDX-License-Identifier: AGPL-3.0-or-later

package messaging

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
)

// RelaySubscriber is the projector_offsets key under which the outbox relay
// records how far it has published the durable log to the in-process bus.
const RelaySubscriber = "outbox-relay"

// relayBatchSize bounds one catch-up read so a large backlog replays in bounded
// chunks rather than loading the whole log into memory.
const relayBatchSize = 500

// defaultRelayInterval is the safety-net cadence: the hot path advances the
// watermark inline, so a periodic pass normally finds nothing and only backfills
// events whose inline advance lost a race or was skipped by a crash.
const defaultRelayInterval = 30 * time.Second

// OutboxRow is one durable event the relay may need to re-publish: its global
// position and its canonical protojson envelope payload.
type OutboxRow struct {
	GlobalSeq int64
	Payload   []byte
}

// outboxReader is the relay's port onto the durable log and its watermark.
// *eventlog.OutboxStore satisfies it.
type outboxReader interface {
	// SeedWatermark sets subscriber's watermark to the current head of the log
	// if (and only if) it has none yet, so a first-ever boot does not replay the
	// entire history.
	SeedWatermark(ctx context.Context, subscriber string) error
	// Watermark returns subscriber's last delivered global_seq (0 if unset).
	Watermark(ctx context.Context, subscriber string) (int64, error)
	// SetWatermark records subscriber's last delivered global_seq.
	SetWatermark(ctx context.Context, subscriber string, globalSeq int64) error
	// EventsAfter returns up to limit events with global_seq greater than after,
	// ordered by global_seq ascending.
	EventsAfter(ctx context.Context, after int64, limit int32) ([]OutboxRow, error)
}

// Relay is the transactional-outbox pump: it re-publishes durable events the
// in-process bus may not have delivered before a restart, in strict global_seq
// order, advancing a single delivery watermark as it goes. It closes the gap the
// in-memory bus leaves — an event appended but not delivered when the process
// dies is delivered on the next boot instead of lost (E1).
type Relay struct {
	reader   outboxReader
	pub      message.Publisher
	wake     <-chan struct{}
	interval time.Duration
	logger   *slog.Logger
}

// NewRelay builds a relay that re-publishes to pub (the same in-process bus its
// consumers subscribe to).
func NewRelay(reader outboxReader, pub message.Publisher, wake <-chan struct{}, logger *slog.Logger) *Relay {
	return &Relay{
		reader:   reader,
		pub:      pub,
		wake:     wake,
		interval: defaultRelayInterval,
		logger:   logger,
	}
}

// Seed pins the watermark to the head of the log on a first-ever boot so the
// relay never re-blasts historical side-effects (emails, DMs). Idempotent: a
// no-op once the watermark exists. Call once, before any consumer starts.
func (r *Relay) Seed(ctx context.Context) error {
	if err := r.reader.SeedWatermark(ctx, RelaySubscriber); err != nil {
		return fmt.Errorf("seeding outbox watermark: %w", err)
	}

	return nil
}

// Run catches up immediately, then re-checks on a ticker until ctx is cancelled.
func (r *Relay) Run(ctx context.Context) {
	if n, err := r.catchUp(ctx); err != nil {
		r.logger.ErrorContext(ctx, "outbox relay: boot catch-up failed", slog.Any("error", err))
	} else if n > 0 {
		r.logger.InfoContext(ctx, "outbox relay: re-published undelivered events on boot", slog.Int("count", n))
	}

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
			if _, err := r.catchUp(ctx); err != nil {
				r.logger.WarnContext(ctx, "outbox relay: notified catch-up failed", slog.Any("error", err))
			}
		case <-ticker.C:
			if _, err := r.catchUp(ctx); err != nil {
				r.logger.WarnContext(ctx, "outbox relay: catch-up failed", slog.Any("error", err))
			}
		}
	}
}

// catchUp re-publishes every event above the watermark, in order, in bounded
// batches, advancing the watermark after each. It returns the count published.
// A publish or store error stops the pass at the current watermark so the next
// pass resumes from exactly there — never skipping an event.
func (r *Relay) catchUp(ctx context.Context) (int, error) {
	published := 0

	for {
		if err := ctx.Err(); err != nil {
			return published, err
		}

		watermark, err := r.reader.Watermark(ctx, RelaySubscriber)
		if err != nil {
			return published, fmt.Errorf("reading watermark: %w", err)
		}

		rows, err := r.reader.EventsAfter(ctx, watermark, relayBatchSize)
		if err != nil {
			return published, fmt.Errorf("reading events after %d: %w", watermark, err)
		}

		if len(rows) == 0 {
			return published, nil
		}

		for _, row := range rows {
			if err := r.publish(ctx, row); err != nil {
				return published, fmt.Errorf("re-publishing global_seq %d: %w", row.GlobalSeq, err)
			}

			if err := r.reader.SetWatermark(ctx, RelaySubscriber, row.GlobalSeq); err != nil {
				return published, fmt.Errorf("advancing watermark to %d: %w", row.GlobalSeq, err)
			}

			published++
		}
	}
}

// publish reconstructs the wire message from the stored protojson payload — byte
// for byte how the hot path publishes it — and delivers it to the bus.
func (r *Relay) publish(ctx context.Context, row OutboxRow) error {
	env := &orakov1.Envelope{}
	if err := protojson.Unmarshal(row.Payload, env); err != nil {
		return fmt.Errorf("decoding payload: %w", err)
	}

	data, err := proto.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshaling envelope: %w", err)
	}

	msg := message.NewMessage(env.GetEventId(), data)
	msg.Metadata.Set(metadataGlobalSeq, strconv.FormatInt(row.GlobalSeq, 10))
	msg.SetContext(context.WithoutCancel(ctx))

	if err := r.pub.Publish(EventsTopic, msg); err != nil {
		return fmt.Errorf("publishing to bus: %w", err)
	}

	return nil
}
