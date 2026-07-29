-- SPDX-License-Identifier: AGPL-3.0-or-later

-- name: appendEvent :one
-- appendEvent inserts one event at the next per-project sequence position. The
-- subselect derives seq atomically within the insert; the (project_id, seq)
-- unique constraint turns a concurrent race into a conflict the adapter maps
-- to a sentinel rather than a silent duplicate.
INSERT INTO event_log (id, project_id, seq, type, payload, occurred_at)
VALUES (
    $1,
    $2,
    (SELECT COALESCE(MAX(seq), 0) + 1 FROM event_log WHERE project_id = $2),
    $3,
    $4,
    $5
)
RETURNING id, project_id, seq, type, payload, occurred_at, created_at, global_seq;

-- name: eventsByProjectSince :many
SELECT id, project_id, seq, type, payload, occurred_at, created_at, global_seq
FROM event_log
WHERE project_id = $1 AND seq > $2
ORDER BY seq ASC;

-- name: eventsAfterGlobalSeq :many
-- Ordered replay window for the outbox relay (E1): events the bus may not have
-- delivered before a restart, above a subscriber's watermark.
SELECT global_seq, id, project_id, seq, type, payload, occurred_at, created_at
FROM event_log
WHERE global_seq > $1
ORDER BY global_seq ASC
LIMIT $2;
