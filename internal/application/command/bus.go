// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
)

// eventBus is the narrow command-layer port for publishing domain events.
// It is satisfied by *messaging.GoChannelBus without the command package
// importing the concrete adapter type.
//
// Deliberate coupling: this port is typed on the protobuf orakov1.Envelope
// rather than a domain event type. The Envelope plays three roles at once — the
// internal domain event, the Postgres outbox storage payload, and the SSE wire
// format streamed to the dashboard — so command/ and event/ construct protobuf
// directly. Decoupling this (plain-Go domain events + a domain→Envelope mapper
// at the messaging boundary) is an accepted, deferred trade-off, not an
// oversight; it is the one place the application layer touches the wire
// contract. See aidd_docs/memory/internal/decisions/2026-07-20-protobuf-envelope-as-event-type.md.
type eventBus interface {
	Publish(ctx context.Context, env *orakov1.Envelope) (*orakov1.Envelope, error)
}
