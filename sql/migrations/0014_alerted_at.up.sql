-- SPDX-License-Identifier: AGPL-3.0-or-later
-- One-shot marker for the third escalation rung (channel alert): a pool
-- conversation unclaimed past ALERT_TIMEOUT is posted to the alert channel
-- at most once per conversation.
ALTER TABLE conversations
    ADD COLUMN alerted_at TIMESTAMPTZ;
