-- SPDX-License-Identifier: AGPL-3.0-or-later
-- 0042: per-subscriber outbox watermark (E1). On boot the relay replays event_log
-- rows with global_seq greater than each subscriber's stored offset, so a crash
-- between append and delivery re-delivers only the unprocessed tail (consumers are
-- already replay-idempotent). Each subscriber advances its own offset.
CREATE TABLE projector_offsets (
    subscriber      TEXT PRIMARY KEY,
    last_global_seq BIGINT NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
