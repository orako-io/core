-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Escalation knobs, per organization. NULL = product default (30m claim,
-- 2h silence); 0 = that rule disabled for the org.
ALTER TABLE organizations
    ADD COLUMN claim_timeout_seconds   BIGINT,
    ADD COLUMN silence_timeout_seconds BIGINT;

-- One-shot marker: the "still unclaimed" nudge fires at most once per
-- conversation.
ALTER TABLE conversations
    ADD COLUMN nudged_at TIMESTAMPTZ;
