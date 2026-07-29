// SPDX-License-Identifier: AGPL-3.0-or-later

// Package messaging is the watermill event-transport adapter for Orako events.
// It marshals the protobuf event contract, anchors every publish in the durable
// Postgres event log (the source of truth), and delivers it over the in-process
// GoChannel bus (single-node by design; a distributed transport can be added
// back behind the same envelope codec if multi-node ever lands).
package messaging

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

// enrichAndAppend completes an envelope's identity and timing, appends it to the
// durable event log, and returns the enriched envelope with its marshaled wire
// bytes. It is the append-before-publish step shared by every transport
// adapter: the Postgres append is the source of truth and assigns Seq, so it
// must succeed before any wire delivery and a transport failure can never lose
// a durable event.
func enrichAndAppend(
	ctx context.Context,
	store service.EventStore,
	env *orakov1.Envelope,
) (enriched *orakov1.Envelope, wire []byte, globalSeq int64, err error) {
	if env.GetEventId() == "" {
		env.EventId = uuid.NewString()
	}

	if env.GetOccurredAt() == nil {
		env.OccurredAt = timestamppb.Now()
	}

	ev, err := envelopeToEvent(env)
	if err != nil {
		return nil, nil, 0, err
	}

	stored, err := store.Append(ctx, ev)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("appending event to log: %w", err)
	}

	env.Seq = stored.Seq

	data, err := proto.Marshal(env)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("marshaling envelope: %w", err)
	}

	return env, data, stored.GlobalSeq, nil
}

// envelopeToEvent maps a contract Envelope to a domain Event. The payload is
// stored as canonical protojson so the log row is self-describing and
// queryable, while the domain stays free of any protobuf type.
func envelopeToEvent(env *orakov1.Envelope) (model.Event, error) {
	id, err := uuid.Parse(env.GetEventId())
	if err != nil {
		return model.Event{}, fmt.Errorf("parsing event id: %w", err)
	}

	projectID, err := uuid.Parse(env.GetProjectId())
	if err != nil {
		return model.Event{}, fmt.Errorf("parsing project id: %w", err)
	}

	eventType, err := eventTypeToDomain(env.GetType())
	if err != nil {
		return model.Event{}, err
	}

	payload, err := protojson.Marshal(env)
	if err != nil {
		return model.Event{}, fmt.Errorf("marshaling payload: %w", err)
	}

	return model.NewEvent(
		id,
		projectID,
		eventType,
		payload,
		env.GetOccurredAt().AsTime(),
	)
}

// eventTypeByWire maps each contract event type to its domain value.
var eventTypeByWire = map[orakov1.EventType]model.EventType{ //nolint:gochecknoglobals // compile-time constant map; package-level by design
	orakov1.EventType_EVENT_TYPE_CONVERSATION_OPENED:            model.EventTypeConversationOpened,
	orakov1.EventType_EVENT_TYPE_MESSAGE_POSTED:                 model.EventTypeMessagePosted,
	orakov1.EventType_EVENT_TYPE_CONVERSATION_CLOSED:            model.EventTypeConversationClosed,
	orakov1.EventType_EVENT_TYPE_KB_ENTRY_CREATED:               model.EventTypeKBEntryCreated,
	orakov1.EventType_EVENT_TYPE_KB_ANSWER_SUPERSEDED:           model.EventTypeKBAnswerSuperseded,
	orakov1.EventType_EVENT_TYPE_KB_FEEDBACK:                    model.EventTypeKBFeedback,
	orakov1.EventType_EVENT_TYPE_MEMBER_LIFECYCLE:               model.EventTypeMemberLifecycle,
	orakov1.EventType_EVENT_TYPE_PRESENCE_CHANGED:               model.EventTypePresenceChanged,
	orakov1.EventType_EVENT_TYPE_PROVIDER_CONFIGURED:            model.EventTypeProviderConfigured,
	orakov1.EventType_EVENT_TYPE_CONVERSATION_CLAIMED:           model.EventTypeConversationClaimed,
	orakov1.EventType_EVENT_TYPE_CONVERSATION_RELEASED:          model.EventTypeConversationReleased,
	orakov1.EventType_EVENT_TYPE_CONVERSATION_PARTICIPANT_ADDED: model.EventTypeConversationParticipantAdded,
}

// eventTypeToDomain maps a contract event type to its domain value.
func eventTypeToDomain(t orakov1.EventType) (model.EventType, error) {
	if t == orakov1.EventType_EVENT_TYPE_UNSPECIFIED {
		return model.EventTypeUnspecified, errors.New("event type is unspecified")
	}

	domain, ok := eventTypeByWire[t]
	if !ok {
		return model.EventTypeUnspecified, fmt.Errorf("unknown event type %d", t)
	}

	return domain, nil
}

// DecodeEnvelope decodes a transport payload back into a contract Envelope.
func DecodeEnvelope(payload []byte) (*orakov1.Envelope, error) {
	env := &orakov1.Envelope{}
	if err := proto.Unmarshal(payload, env); err != nil {
		return nil, fmt.Errorf("unmarshaling envelope: %w", err)
	}

	return env, nil
}
