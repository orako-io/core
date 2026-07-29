-- SPDX-License-Identifier: AGPL-3.0-or-later
-- 0041: global monotonic position on the append-only event log (E2). Gives the
-- outbox relay (E1) a single per-subscriber watermark across all project streams.
-- BIGSERIAL backfills existing rows and auto-assigns on future inserts.
ALTER TABLE event_log ADD COLUMN global_seq BIGSERIAL;
CREATE UNIQUE INDEX idx_event_log_global_seq ON event_log (global_seq);
