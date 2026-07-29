-- SPDX-License-Identifier: AGPL-3.0-or-later

DROP INDEX IF EXISTS idx_project_members_domains;
DROP INDEX IF EXISTS idx_conversations_sweeper_expiry;
DROP INDEX IF EXISTS idx_conversations_sweeper_alert;
DROP INDEX IF EXISTS idx_conversations_sweeper_nudge;

-- Re-add the refs column with its original type/default so a rollback restores
-- the pre-0052 schema. Existing rows get the '{}' default.
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS refs jsonb NOT NULL DEFAULT '{}'::jsonb;
