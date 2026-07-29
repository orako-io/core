// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// EventType discriminates the payload an event carries. It is the domain mirror
// of orako.v1.EventType and must never import the generated proto package.
// Values mirror the proto enum numbering for lossless cross-layer mapping.
type EventType int32

// Recognized event types. Values mirror orako.v1.EventType.
const (
	EventTypeUnspecified        EventType = 0
	EventTypeConversationOpened EventType = 1
	EventTypeMessagePosted      EventType = 2
	EventTypeConversationClosed EventType = 3
	EventTypeKBEntryCreated     EventType = 4
	EventTypeKBAnswerSuperseded EventType = 5
	EventTypeKBFeedback         EventType = 6
	EventTypeMemberLifecycle    EventType = 7
	EventTypePresenceChanged    EventType = 8
	EventTypeProviderConfigured EventType = 9
	// EventTypeConversationClaimed marks a pool conversation taken by a
	// candidate; EventTypeConversationReleased marks a claim given back
	// (silence escalation).
	EventTypeConversationClaimed  EventType = 10
	EventTypeConversationReleased EventType = 11
	// EventTypeConversationParticipantAdded marks a member explicitly added to
	// an existing conversation (they receive the thread history).
	EventTypeConversationParticipantAdded EventType = 12
)

// Valid reports whether t is a recognized, non-zero event type.
func (t EventType) Valid() bool {
	switch t {
	case EventTypeUnspecified:
		return false
	case EventTypeConversationOpened,
		EventTypeMessagePosted,
		EventTypeConversationClosed,
		EventTypeKBEntryCreated,
		EventTypeKBAnswerSuperseded,
		EventTypeKBFeedback,
		EventTypeMemberLifecycle,
		EventTypePresenceChanged,
		EventTypeProviderConfigured,
		EventTypeConversationClaimed,
		EventTypeConversationReleased,
		EventTypeConversationParticipantAdded:
		return true
	}

	return false
}

// eventTypeNames are the snake_case names persisted to the database type
// column, one per recognized event type.
var eventTypeNames = map[EventType]string{ //nolint:gochecknoglobals // compile-time constant map; package-level by design
	EventTypeConversationOpened:           "conversation_opened",
	EventTypeMessagePosted:                "message_posted",
	EventTypeConversationClosed:           "conversation_closed",
	EventTypeKBEntryCreated:               "kb_entry_created",
	EventTypeKBAnswerSuperseded:           "kb_answer_superseded",
	EventTypeKBFeedback:                   "kb_feedback",
	EventTypeMemberLifecycle:              "member_lifecycle",
	EventTypePresenceChanged:              "presence_changed",
	EventTypeProviderConfigured:           "provider_configured",
	EventTypeConversationClaimed:          "conversation_claimed",
	EventTypeConversationReleased:         "conversation_released",
	EventTypeConversationParticipantAdded: "conversation_participant_added",
}

// String returns the snake_case name persisted to the database type column.
func (t EventType) String() string {
	if name, ok := eventTypeNames[t]; ok {
		return name
	}

	return strUnspecified
}

// Event is a single project-scoped, sequenced fact in the Orako's append-only
// log. Payload holds the typed body encoded to a transport-neutral byte form
// (JSON of the proto payload), so the domain never depends on the wire format.
// Seq is the per-project monotonic position the event store assigns on append;
// it is zero until then.
type Event struct {
	// ID uniquely identifies the event.
	ID uuid.UUID
	// ProjectID scopes the event to a project; drives log partitioning.
	ProjectID uuid.UUID
	// Seq is the per-project position assigned by the store (zero before append).
	Seq int64
	// GlobalSeq is the store-wide monotonic position assigned by the store (zero
	// before append). It orders events across every project for the outbox relay.
	GlobalSeq int64
	// Type discriminates the payload.
	Type EventType
	// Payload is the transport-neutral JSONB-encoded body.
	Payload []byte
	// OccurredAt is when the event happened at the source.
	OccurredAt time.Time
}

// NewEvent builds an Event for appending, validating every invariant. Seq is
// intentionally not an input: the event store owns sequence assignment.
func NewEvent(
	id uuid.UUID,
	projectID uuid.UUID,
	eventType EventType,
	payload []byte,
	occurredAt time.Time,
) (Event, error) {
	if id == uuid.Nil {
		return Event{}, errs.InvalidError{Field: "id", Reason: nilUUIDReason}
	}

	if projectID == uuid.Nil {
		return Event{}, errs.InvalidError{Field: "project_id", Reason: nilUUIDReason}
	}

	if !eventType.Valid() {
		return Event{}, errs.InvalidError{Field: "type", Reason: "unknown event type"}
	}

	if len(payload) == 0 {
		return Event{}, errs.InvalidError{Field: "payload", Reason: emptyReason}
	}

	if occurredAt.IsZero() {
		return Event{}, errs.InvalidError{Field: "occurred_at", Reason: "must be set"}
	}

	return Event{
		ID:         id,
		ProjectID:  projectID,
		Seq:        0, // assigned by the store on append
		GlobalSeq:  0, // assigned by the store on append
		Type:       eventType,
		Payload:    payload,
		OccurredAt: occurredAt,
	}, nil
}
