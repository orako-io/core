// SPDX-License-Identifier: AGPL-3.0-or-later

// Package api — SSE event stream: pushes the internal event bus to the
// dashboard, filtered by the caller's project memberships. The browser is the
// only push target; agents stay pull-based (realtime.md invariant).
package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/adapters/messaging"
)

// ssePingInterval keeps intermediaries from timing the stream out.
const ssePingInterval = 20 * time.Second

// EventsSSEHandler streams bus envelopes as Server-Sent Events.
type EventsSSEHandler struct {
	authn       Authenticator
	subscriber  message.Subscriber
	memberships membershipReader
	logger      *slog.Logger
}

// NewEventsSSEHandler builds the handler over the shared authenticator, the
// in-process bus subscriber, and the membership reader used for filtering.
func NewEventsSSEHandler(authn Authenticator, subscriber message.Subscriber, memberships membershipReader, logger *slog.Logger) *EventsSSEHandler {
	return &EventsSSEHandler{authn: authn, subscriber: subscriber, memberships: memberships, logger: logger}
}

// ServeHTTP authenticates, resolves the caller's projects, then streams every
// envelope belonging to one of them until the client disconnects.
func (h *EventsSSEHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	identity, err := h.authn.Authenticate(r.Context(), r.Header.Get("Authorization"))
	if err != nil || identity.MemberID == uuid.Nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)

		return
	}

	allowed, err := h.allowedProjects(r, identity)
	if err != nil {
		h.logger.WarnContext(r.Context(), "events stream: resolving memberships failed", slog.Any("error", err))
		http.Error(w, "resolving memberships failed", http.StatusInternalServerError)

		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)

		return
	}

	messages, err := h.subscriber.Subscribe(r.Context(), messaging.EventsTopic)
	if err != nil {
		h.logger.WarnContext(r.Context(), "events stream: subscribing failed", slog.Any("error", err))
		http.Error(w, "subscribing failed", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// An immediate comment confirms the stream to the client before the
	// first real event. A write error anywhere below means the client is
	// gone: stop streaming.
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}

	flusher.Flush()

	h.stream(r.Context(), w, flusher, messages, allowed)
}

// stream pumps bus messages (and keep-alive pings) to the client until the
// connection dies or the context is cancelled.
func (h *EventsSSEHandler) stream(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, messages <-chan *message.Message, allowed map[uuid.UUID]struct{}) {
	ping := time.NewTicker(ssePingInterval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}

			flusher.Flush()

		case msg, open := <-messages:
			if !open {
				return
			}

			err := h.writeEvent(w, flusher, msg, allowed)
			msg.Ack() // the bus must never re-deliver to a dead connection

			if err != nil {
				return
			}
		}
	}
}

// writeEvent renders one bus message as an SSE frame when the caller may see
// it. A non-nil error means the client connection is dead; undecodable or
// foreign messages are dropped without error.
func (h *EventsSSEHandler) writeEvent(w http.ResponseWriter, flusher http.Flusher, msg *message.Message, allowed map[uuid.UUID]struct{}) error {
	env, ok := envelopeFor(msg, allowed)
	if !ok {
		return nil
	}

	payload, err := protojson.Marshal(env)
	if err != nil {
		// Serialization is a server-side hiccup, never a client death.
		h.logger.Warn("events stream: marshalling envelope failed", slog.Any("error", err))

		return nil
	}

	// protojson output has no newlines, so a single data: line is safe.
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", sseEventName(env.GetType().String()), payload); err != nil {
		return fmt.Errorf("writing SSE frame: %w", err)
	}

	flusher.Flush()

	return nil
}

// envelopeFor decodes a bus message and applies the caller's project filter;
// ok is false for anything undecodable or foreign.
func envelopeFor(msg *message.Message, allowed map[uuid.UUID]struct{}) (*orakov1.Envelope, bool) {
	env, err := messaging.DecodeEnvelope(msg.Payload)
	if err != nil {
		return nil, false
	}

	projectID, err := uuid.Parse(env.GetProjectId())
	if err != nil {
		return nil, false
	}

	if _, ok := allowed[projectID]; !ok {
		return nil, false
	}

	return env, true
}

// allowedProjects resolves the caller's project set for this connection.
// Reconnects re-resolve, so a fresh membership is picked up within one
// reconnect cycle.
func (h *EventsSSEHandler) allowedProjects(r *http.Request, identity CallerIdentity) (map[uuid.UUID]struct{}, error) {
	memberships, err := h.memberships.ProjectsByMember(r.Context(), identity.MemberID)
	if err != nil {
		return nil, err
	}

	allowed := make(map[uuid.UUID]struct{}, len(memberships))
	for _, m := range memberships {
		allowed[m.ID] = struct{}{}
	}

	return allowed, nil
}

// sseEventName maps EVENT_TYPE_CONVERSATION_OPENED → conversation_opened —
// the stable names the web client subscribes to.
func sseEventName(enumName string) string {
	return strings.ToLower(strings.TrimPrefix(enumName, "EVENT_TYPE_"))
}
