-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0050_db_audit_remediation (down): reverse the audit remediation.

-- 4. Recreate kb_feedback as it stood immediately before the up migration.
--    NOTE: the original 0001 definition had `kb_entry_id ... REFERENCES
--    kb_entries (id)`, but 0046 dropped kb_entries (CASCADE also dropped this FK
--    while leaving the table). The pre-up state therefore has kb_entry_id as a
--    plain column with no FK, so this restore omits the kb_entries reference —
--    re-adding it would fail because kb_entries no longer exists. The member_id
--    FK is restored verbatim.
CREATE TABLE IF NOT EXISTS kb_feedback (
    id          UUID PRIMARY KEY,
    kb_entry_id UUID NOT NULL,
    member_id   UUID NOT NULL REFERENCES members (id),
    verdict     TEXT NOT NULL,  -- useful|inaccurate
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 3. Restore the original chat-binding unique indexes (no status predicate).
DROP INDEX IF EXISTS idx_members_teams_user_id_unique;
CREATE UNIQUE INDEX idx_members_teams_user_id_unique
    ON members (teams_user_id) WHERE teams_user_id <> '';

DROP INDEX IF EXISTS idx_members_discord_user_id_unique;
CREATE UNIQUE INDEX idx_members_discord_user_id_unique
    ON members (discord_user_id) WHERE discord_user_id <> '';

DROP INDEX IF EXISTS idx_members_telegram_chat_id_unique;
CREATE UNIQUE INDEX idx_members_telegram_chat_id_unique
    ON members (telegram_chat_id) WHERE telegram_chat_id IS NOT NULL;

-- 2. Drop the members(email) lookup index.
DROP INDEX IF EXISTS idx_members_email;

-- 1. Drop the FK covering indexes.
DROP INDEX IF EXISTS idx_project_members_member_id;
DROP INDEX IF EXISTS idx_conversations_responder_member_id;
DROP INDEX IF EXISTS idx_conversations_asker_member_id;
DROP INDEX IF EXISTS idx_messages_author_member_id;
DROP INDEX IF EXISTS idx_provider_messages_member_id;
DROP INDEX IF EXISTS idx_conversation_participants_added_by;
DROP INDEX IF EXISTS idx_attachments_uploaded_by_member_id;
DROP INDEX IF EXISTS idx_attachments_project_id;
DROP INDEX IF EXISTS idx_oauth_authorization_codes_member_id;
DROP INDEX IF EXISTS idx_oauth_authorization_codes_client_id;
DROP INDEX IF EXISTS idx_oauth_tokens_client_id;
DROP INDEX IF EXISTS idx_org_join_tokens_project_id;
