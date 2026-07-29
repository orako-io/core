// SPDX-License-Identifier: AGPL-3.0-or-later

package messaging_test

import (
	"context"
	"log/slog"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/orako-io/core/internal/adapters/eventlog"
	"github.com/orako-io/core/internal/adapters/messaging"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestGoChannelRoundTrip: an event published through the in-process GoChannel
// bus is received by a subscriber decoded equal to the input AND is durably
// present in the Postgres event log first. The goleak gate in TestMain asserts
// the bus leaves no goroutine behind after Close.
func TestGoChannelRoundTrip(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	logger := slog.New(slog.DiscardHandler)

	projectID := seedProject(t, pool)
	store := eventlog.NewStore(pool)
	outbox := eventlog.NewOutboxStore(pool)
	wake := make(chan struct{}, 1)

	bus := messaging.NewGoChannelBus(store, wake, logger)
	relay := messaging.NewRelay(outbox, bus.Publisher(), wake, logger)

	t.Cleanup(func() { _ = bus.Close() })

	messages, err := bus.Subscriber().Subscribe(t.Context(), messaging.EventsTopic)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := relay.Seed(t.Context()); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	relayCtx, cancelRelay := context.WithCancel(t.Context())
	t.Cleanup(cancelRelay)
	go relay.Run(relayCtx)

	published, err := bus.Publish(t.Context(), presenceEnvelope(projectID))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if published.GetSeq() != 1 {
		t.Fatalf("published Seq = %d, want 1", published.GetSeq())
	}

	received := receiveEnvelope(t, messages)

	if !proto.Equal(published, received) {
		t.Fatalf("received envelope differs from published:\n got: %v\nwant: %v", received, published)
	}

	assertPersisted(t, store, projectID, published)
}
