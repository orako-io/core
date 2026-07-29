// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// EventStore is the write/read port for the append-only event log. Driven
// adapters under internal/adapters implement it; the domain owns the contract.
//
// The store is the Orako's source of truth: an event is durable once Append
// returns. Implementations expose only the sentinel errors from
// internal/adapters/errors, never raw driver errors.
type EventStore interface {
	// Append durably stores ev at the next per-project sequence position and
	// returns the stored event with its assigned Seq. The input ev.Seq is
	// ignored; the store owns sequencing.
	//
	// A uniqueness collision on (project_id, seq) from a concurrent append
	// surfaces as adaptererr.ErrDuplicate, which the caller may retry.
	Append(ctx context.Context, ev model.Event) (model.Event, error)
	// Replay returns every event in projectID with Seq greater than sinceSeq,
	// in ascending sequence order. Pass sinceSeq = 0 to replay the whole
	// project log.
	Replay(ctx context.Context, projectID uuid.UUID, sinceSeq int64) ([]model.Event, error)
}
