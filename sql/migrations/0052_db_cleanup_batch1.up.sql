-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0052_db_cleanup_batch1: DB cleanup batch 1 (2026-07-26 audit).
--
--   1. Drop the dead conversations.refs column. It was always written as '{}'
--      and never populated — the MCP folds refs into the context packet, so no
--      code path reads it. Domain/read models never carried it.
--   2. Partial indexes for the per-minute escalation sweeper. Each rung scans
--      open, unassigned pool conversations by its once-only marker; without
--      these the sweep seq-scans conversations every minute. The expiry rung
--      (new in this batch, status='timed_out' transition) has no marker column
--      of its own — its CAS flips status off 'open' — so its index predicate is
--      just the open/unassigned scan.
--   3. GIN on project_members.domains: `domains && $2` array-overlap runs on
--      every candidate-resolution dispatch and had no supporting index.
--
-- Data is tiny today, so plain CREATE INDEX (no CONCURRENTLY) builds instantly;
-- at scale switch to CONCURRENTLY.

-- 1. Drop the dead refs column.
ALTER TABLE conversations DROP COLUMN IF EXISTS refs;

-- 2. Escalation sweeper partial indexes.
CREATE INDEX IF NOT EXISTS idx_conversations_sweeper_nudge
    ON conversations (created_at)
    WHERE responder_member_id IS NULL AND status = 'open' AND nudged_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_conversations_sweeper_alert
    ON conversations (created_at)
    WHERE responder_member_id IS NULL AND status = 'open' AND alerted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_conversations_sweeper_expiry
    ON conversations (created_at)
    WHERE responder_member_id IS NULL AND status = 'open';

-- 3. Array-overlap index for candidate resolution at dispatch.
CREATE INDEX IF NOT EXISTS idx_project_members_domains
    ON project_members USING gin (domains);
