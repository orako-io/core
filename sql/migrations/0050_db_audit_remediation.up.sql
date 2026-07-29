-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0050_db_audit_remediation: acts on the 2026-07-26 DB audit.
--
--   1. Cover the 12 foreign keys that had no index on the referencing column, so
--      parent deletes and reverse lookups stop full-scanning the child. Data is
--      tiny today, so a plain CREATE INDEX (no CONCURRENTLY) builds instantly;
--      at scale switch to CONCURRENTLY.
--   2. Index members(email) for the by-email fallback (Slack directory).
--   3. Recreate the discord/telegram/teams partial unique indexes so a dead
--      member (removed/purged/deactivated/suspended) no longer reserves a chat id
--      forever — the id is free for a live member to rebind.
--   4. Drop the dead kb_feedback table (KB removed in 0046; feedback flows through
--      event_log; no sqlc query references it).

-- 1. Covering indexes for unindexed foreign keys.
CREATE INDEX IF NOT EXISTS idx_project_members_member_id
    ON project_members (member_id);
CREATE INDEX IF NOT EXISTS idx_conversations_responder_member_id
    ON conversations (responder_member_id);
CREATE INDEX IF NOT EXISTS idx_conversations_asker_member_id
    ON conversations (asker_member_id);
CREATE INDEX IF NOT EXISTS idx_messages_author_member_id
    ON messages (author_member_id);
CREATE INDEX IF NOT EXISTS idx_provider_messages_member_id
    ON provider_messages (member_id);
CREATE INDEX IF NOT EXISTS idx_conversation_participants_added_by
    ON conversation_participants (added_by);
CREATE INDEX IF NOT EXISTS idx_attachments_uploaded_by_member_id
    ON attachments (uploaded_by_member_id);
CREATE INDEX IF NOT EXISTS idx_attachments_project_id
    ON attachments (project_id);
CREATE INDEX IF NOT EXISTS idx_oauth_authorization_codes_member_id
    ON oauth_authorization_codes (member_id);
CREATE INDEX IF NOT EXISTS idx_oauth_authorization_codes_client_id
    ON oauth_authorization_codes (client_id);
CREATE INDEX IF NOT EXISTS idx_oauth_tokens_client_id
    ON oauth_tokens (client_id);
CREATE INDEX IF NOT EXISTS idx_org_join_tokens_project_id
    ON org_join_tokens (project_id);

-- 2. Members-by-email lookup (partial: skip the '' / NULL non-addresses).
CREATE INDEX IF NOT EXISTS idx_members_email
    ON members (email) WHERE email IS NOT NULL AND email <> '';

-- 3. Recreate the chat-binding unique indexes so dead members release their id.
--    Same empty/NULL predicate per column as 0015, plus a status guard: an
--    offboarded row is excluded from the uniqueness set, so a live member can
--    rebind the freed id. The code path that offboards a member also blanks the
--    binding columns (see RemoveMemberHandler / offboardMember), so in practice a
--    dead row carries no id — this predicate is the belt-and-braces guarantee.
DROP INDEX IF EXISTS idx_members_teams_user_id_unique;
CREATE UNIQUE INDEX idx_members_teams_user_id_unique
    ON members (teams_user_id)
    WHERE teams_user_id <> ''
      AND status NOT IN ('removed', 'purged', 'deactivated', 'suspended');

DROP INDEX IF EXISTS idx_members_discord_user_id_unique;
CREATE UNIQUE INDEX idx_members_discord_user_id_unique
    ON members (discord_user_id)
    WHERE discord_user_id <> ''
      AND status NOT IN ('removed', 'purged', 'deactivated', 'suspended');

DROP INDEX IF EXISTS idx_members_telegram_chat_id_unique;
CREATE UNIQUE INDEX idx_members_telegram_chat_id_unique
    ON members (telegram_chat_id)
    WHERE telegram_chat_id IS NOT NULL
      AND status NOT IN ('removed', 'purged', 'deactivated', 'suspended');

-- 4. Drop the dead knowledge-base feedback table. 0046 dropped kb_entries with
--    CASCADE, which removed kb_feedback's FK to it but left the table itself
--    (with 0 rows) behind.
DROP TABLE IF EXISTS kb_feedback;
