// SPDX-License-Identifier: AGPL-3.0-or-later

// Package eventlog is the Postgres adapter for the append-only event log itself:
// the event Store (per-project gapless append + global sequence) and the
// OutboxStore watermark behind the transactional outbox relay. The per-aggregate
// projection stores that once lived here have moved to their own bounded-context
// adapters (identity, conversation, billing, integration, presence);
// only the org→provider resolving loader remains as a small cross-cutting helper.
//
// Its generated *.gen.go / *.sql.go files are the typed sqlc query layer for the
// event_log and projector_offsets tables (never hand-edited).
package eventlog
