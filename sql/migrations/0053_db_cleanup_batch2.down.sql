-- SPDX-License-Identifier: AGPL-3.0-or-later

-- Drop the `<> ''` telegram unique index added by the up migration.
DROP INDEX IF EXISTS idx_members_telegram_chat_id_unique;

-- Revert the chat-binding columns to NULLable without a default (their pre-0053
-- shape).
ALTER TABLE members
    ALTER COLUMN slack_user_id DROP NOT NULL,
    ALTER COLUMN slack_user_id DROP DEFAULT,
    ALTER COLUMN telegram_chat_id DROP NOT NULL,
    ALTER COLUMN telegram_chat_id DROP DEFAULT;

-- Restore the NULL "unset" sentinel: pre-0053 an empty binding was stored as
-- NULL (TextOrNull folded '' to NULL), so fold '' back to NULL. This is also
-- required before recreating the old telegram index below — its IS NOT NULL
-- predicate indexes '' and would collide on the backfilled empty strings.
UPDATE members SET slack_user_id = NULL WHERE slack_user_id = '';
UPDATE members SET telegram_chat_id = NULL WHERE telegram_chat_id = '';

-- Recreate the telegram partial unique index on its 0050 IS NOT NULL predicate.
CREATE UNIQUE INDEX idx_members_telegram_chat_id_unique
    ON members (telegram_chat_id)
    WHERE telegram_chat_id IS NOT NULL
      AND status NOT IN ('removed', 'purged', 'deactivated', 'suspended');

-- Drop the CHECK constraints added in the up migration.
ALTER TABLE org_members DROP CONSTRAINT IF EXISTS org_members_role_check;
ALTER TABLE project_providers DROP CONSTRAINT IF EXISTS project_providers_kind_check;
ALTER TABLE org_providers DROP CONSTRAINT IF EXISTS org_providers_kind_check;
ALTER TABLE conversation_surfaces DROP CONSTRAINT IF EXISTS conversation_surfaces_kind_check;
ALTER TABLE conversation_surfaces DROP CONSTRAINT IF EXISTS conversation_surfaces_provider_check;
ALTER TABLE members DROP CONSTRAINT IF EXISTS members_status_check;
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_source_check;
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_role_check;
ALTER TABLE conversations DROP CONSTRAINT IF EXISTS conversations_status_check;
