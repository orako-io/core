-- SPDX-License-Identifier: AGPL-3.0-or-later

-- name: getProjectorOffset :one
SELECT last_global_seq FROM projector_offsets WHERE subscriber = $1;

-- name: upsertProjectorOffset :exec
INSERT INTO projector_offsets (subscriber, last_global_seq, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (subscriber) DO UPDATE
SET last_global_seq = EXCLUDED.last_global_seq, updated_at = NOW();

-- name: seedProjectorOffset :exec
-- Seed a subscriber's watermark to the current head of the log so a first-ever
-- boot does NOT replay (and re-deliver) the entire historical log. No-op once
-- the subscriber already has an offset.
INSERT INTO projector_offsets (subscriber, last_global_seq, updated_at)
SELECT $1, COALESCE(MAX(global_seq), 0), NOW() FROM event_log
ON CONFLICT (subscriber) DO NOTHING;

-- name: advanceProjectorOffset :execrows
-- Contiguous advance used by the publish hot path: moves the watermark forward
-- only when it currently sits at from_seq, so a concurrent/out-of-order publish
-- can never let the watermark skip an un-delivered event (the relay's ordered
-- catch-up fills any gap). Rows affected = 0 means "not contiguous, leave it for
-- the relay".
UPDATE projector_offsets
SET last_global_seq = @to_seq, updated_at = NOW()
WHERE subscriber = @subscriber AND last_global_seq = @from_seq;
