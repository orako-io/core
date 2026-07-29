// SPDX-License-Identifier: AGPL-3.0-or-later

package messaging_test

import (
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/adapters/eventlog"
	"github.com/orako-io/core/internal/adapters/messaging"
)

// receiveTimeout bounds how long a test waits for an event to traverse the bus.
const receiveTimeout = 10 * time.Second

// receiveEnvelope returns the decoded envelope, failing the test on timeout.
func receiveEnvelope(t *testing.T, messages <-chan *message.Message) *orakov1.Envelope {
	t.Helper()

	select {
	case msg := <-messages:
		env, err := messaging.DecodeEnvelope(msg.Payload)
		if err != nil {
			t.Fatalf("DecodeEnvelope: %v", err)
		}

		msg.Ack()

		return env
	case <-time.After(receiveTimeout):
		t.Fatal("did not receive the event before timeout")

		return nil
	}
}

// assertPersisted fails unless want is durably present in the project's event log.
func assertPersisted(t *testing.T, store *eventlog.Store, projectID uuid.UUID, want *orakov1.Envelope) {
	t.Helper()

	events, err := store.Replay(t.Context(), projectID, 0)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	for _, ev := range events {
		if ev.ID.String() == want.GetEventId() {
			if ev.Seq != want.GetSeq() {
				t.Fatalf("persisted Seq = %d, want %d", ev.Seq, want.GetSeq())
			}

			return
		}
	}

	t.Fatalf("event %s not found in event_log (%d events)", want.GetEventId(), len(events))
}

// presenceEnvelope builds a PresenceChanged event for projectID.
func presenceEnvelope(projectID uuid.UUID) *orakov1.Envelope {
	return &orakov1.Envelope{
		ProjectId: projectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_PRESENCE_CHANGED,
		Payload: &orakov1.Envelope_PresenceChanged{
			PresenceChanged: &orakov1.PresenceChanged{
				MemberId: uuid.NewString(),
				Online:   true,
			},
		},
	}
}

// seedProject inserts a project row so event appends satisfy the foreign-key constraint.
func seedProject(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()

	projectID := uuid.New()

	_, err := pool.Exec(t.Context(),
		"INSERT INTO projects (id, name) VALUES ($1, $2)", projectID, "gochannel-test")
	if err != nil {
		t.Fatalf("seedProject: %v", err)
	}

	return projectID
}
