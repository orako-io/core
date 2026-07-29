// SPDX-License-Identifier: AGPL-3.0-or-later

package messaging

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/postgres"
)

// EventsTopic is the single in-process topic the GoChannel bus delivers every
// event on: the single-node server has one consumer group, so every event
// shares one topic rather than a per-project subject.
const EventsTopic = "orako.events"

// metadataGlobalSeq carries an event's store-wide position on the wire message.
const metadataGlobalSeq = "global_seq"

// outputBuffer sizes each subscriber's delivery channel so a transient slow
// consumer does not block the publisher.
const outputBuffer = 256

// GoChannelBus is the single-node event transport: an in-process watermill
// GoChannel pub/sub anchored by the durable Postgres log, which stays the
// source of truth.
//
// GoChannel keeps no global state, so the same instance must publish and
// subscribe; the bus owns one channel shared by Publish and Subscriber.
type GoChannelBus struct {
	bus   *gochannel.GoChannel
	store service.EventStore
	wake  chan<- struct{}
}

// NewGoChannelBus builds an outbox-first event bus backed by store. Publish only
// appends durably and wakes the relay; the relay is the sole component allowed
// to deliver and advance its watermark.
func NewGoChannelBus(store service.EventStore, wake chan<- struct{}, logger *slog.Logger) *GoChannelBus {
	bus := gochannel.NewGoChannel(gochannel.Config{
		OutputChannelBuffer:            outputBuffer,
		PreserveContext:                true,
		BlockPublishUntilSubscriberAck: true,
	}, watermill.NewSlogLogger(logger))

	return &GoChannelBus{
		bus:   bus,
		store: store,
		wake:  wake,
	}
}

// Publish durably logs env and wakes the relay after the surrounding transaction
// commits. It never delivers directly, so mutation + event append can share one
// transaction without consumers observing uncommitted state.
//
// It enriches the envelope, appends it to the event log (which assigns Seq and
// is the source of truth), and only then publishes it to the in-process bus, so
// a delivery failure can never lose a durable event. It returns the enriched
// envelope carrying the assigned sequence.
func (b *GoChannelBus) Publish(ctx context.Context, env *orakov1.Envelope) (*orakov1.Envelope, error) {
	enriched, _, _, err := enrichAndAppend(ctx, b.store, env)
	if err != nil {
		return nil, err
	}

	if !postgres.AfterCommit(ctx, b.notifyRelay) {
		b.notifyRelay()
	}

	return enriched, nil
}

func (b *GoChannelBus) notifyRelay() {
	if b.wake == nil {
		return
	}

	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// Subscriber returns the watermill subscriber for the in-process bus. It is the
// same GoChannel instance Publish uses, as GoChannel requires.
func (b *GoChannelBus) Subscriber() message.Subscriber {
	return b.bus
}

// Publisher returns the raw in-process publisher. The outbox relay uses it to
// re-deliver already-appended events without re-appending them to the log.
func (b *GoChannelBus) Publisher() message.Publisher {
	return b.bus
}

// Close releases the bus and its subscriber goroutines.
func (b *GoChannelBus) Close() error {
	if err := b.bus.Close(); err != nil {
		return fmt.Errorf("closing in-process bus: %w", err)
	}

	return nil
}
