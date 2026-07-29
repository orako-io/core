-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0015_provider_bridge_integrity: 0013 added teams_user_id/discord_user_id
-- with no uniqueness constraint, and the inbound lookups (memberByTeamsUserID,
-- memberByDiscordUserID) are :one with no ORDER BY — a duplicate binding
-- routes an inbound reply to whichever of the two rows Postgres happens to
-- return first, nondeterministically. Partial unique indexes close the gap
-- (the WHERE clause excludes the '' default so unbound members never
-- collide) and double as the missing lookup index for these single-row
-- reads.
--
-- telegram_chat_id has the identical shape (memberByTelegramChatID is also a
-- bare :one) and no existing duplicates, so it gets the same treatment here.
-- It is nullable (not '' default), so the partial predicate is IS NOT NULL.
--
-- slack_user_id is deliberately NOT given a matching constraint: Slack user
-- IDs are workspace-scoped, not globally unique, so a blind global unique
-- index would be the wrong fix (two members bound to different Slack
-- workspaces could legitimately collide on the same short ID) — see the
-- separate, already-tracked BySlackUserID per-workspace-scoping finding.
-- Duplicate slack_user_id values were also observed on the shared dev
-- database at review time, reinforcing that this needs its own scoped fix
-- rather than a mechanical copy of this migration.

CREATE UNIQUE INDEX idx_members_teams_user_id_unique
    ON members (teams_user_id) WHERE teams_user_id <> '';

CREATE UNIQUE INDEX idx_members_discord_user_id_unique
    ON members (discord_user_id) WHERE discord_user_id <> '';

CREATE UNIQUE INDEX idx_members_telegram_chat_id_unique
    ON members (telegram_chat_id) WHERE telegram_chat_id IS NOT NULL;
