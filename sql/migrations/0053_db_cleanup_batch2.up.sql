-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0053_db_cleanup_batch2: schema hardening (2026-07-26 audit, batch 2).
--
--   1. CHECK constraints on the status/kind text columns so a bad adapter or
--      projector write fails loudly at the DB instead of silently persisting a
--      value no read path understands. The value set of each CHECK is the Go
--      domain constant set (the source of truth) — verified against prod's
--      DISTINCT values before adding (every existing value is in the set;
--      'unspecified' sentinels that the domain constructors reject are
--      deliberately excluded). members.delivery_channel, oauth_tokens.kind and
--      provider_messages.state already carried their CHECK from an earlier
--      migration, so they are not re-added here.
--   2. Normalize the members chat-binding "unset" convention: slack_user_id and
--      telegram_chat_id join discord_user_id/teams_user_id on NOT NULL DEFAULT ''
--      (the partial unique indexes already key on `<> ''`). One convention across
--      the four id columns removes the pgtype.Text/NULL special-casing in the
--      store. email and display_name stay NULLable — they are genuinely-optional
--      identity, not chat bindings.
--
-- Data is tiny today, so a plain ADD CONSTRAINT / ALTER COLUMN validates
-- existing rows synchronously and instantly; at scale add NOT VALID + a later
-- VALIDATE CONSTRAINT.

-- 1. CHECK constraints from the Go domain constant sets.

-- conversations.status → model.ConversationStatus
ALTER TABLE conversations
    ADD CONSTRAINT conversations_status_check
    CHECK (status IN ('open', 'answered', 'resolved', 'timed_out', 'dismissed'));

-- messages.role → model.MessageRole (unspecified rejected by NewMessage)
ALTER TABLE messages
    ADD CONSTRAINT messages_role_check
    CHECK (role IN ('question', 'answer', 'follow_up', 'system', 'second_opinion'));

-- messages.source → model.MessageSource
ALTER TABLE messages
    ADD CONSTRAINT messages_source_check
    CHECK (source IN ('human', 'agent', 'system'));

-- members.status → model.MemberStatus
ALTER TABLE members
    ADD CONSTRAINT members_status_check
    CHECK (status IN ('invited', 'pending', 'active', 'on_leave', 'deactivated', 'suspended', 'removed', 'purged'));

-- conversation_surfaces.provider → model.SurfaceProvider* (only discord exists)
ALTER TABLE conversation_surfaces
    ADD CONSTRAINT conversation_surfaces_provider_check
    CHECK (provider IN ('discord'));

-- conversation_surfaces.kind → model.SurfaceKind* (only thread exists)
ALTER TABLE conversation_surfaces
    ADD CONSTRAINT conversation_surfaces_kind_check
    CHECK (kind IN ('thread'));

-- org_providers.kind → provider.ProviderKind
ALTER TABLE org_providers
    ADD CONSTRAINT org_providers_kind_check
    CHECK (kind IN ('slack', 'teams', 'telegram', 'discord', 'noop'));

-- project_providers.kind → provider.ProviderKind
ALTER TABLE project_providers
    ADD CONSTRAINT project_providers_kind_check
    CHECK (kind IN ('slack', 'teams', 'telegram', 'discord', 'noop'));

-- org_members.role → model.OrgRole
ALTER TABLE org_members
    ADD CONSTRAINT org_members_role_check
    CHECK (role IN ('admin', 'member'));

-- 2. Normalize the members chat-binding columns to NOT NULL DEFAULT ''.
--
-- Drop the old telegram partial unique index BEFORE the NULL→'' backfill: its
-- 0050 predicate is `telegram_chat_id IS NOT NULL`, which treats '' as an
-- indexed value, so folding many rows' NULL into '' at once would collide on the
-- empty string against that index. Dropping it first lets the backfill run, then
-- we recreate the index on the `<> ''` convention (matching discord/teams) so ''
-- is the unindexed "unset" sentinel. The dead-status exclusion from 0050 is kept
-- so an offboarded row still releases its id.
DROP INDEX IF EXISTS idx_members_telegram_chat_id_unique;

UPDATE members SET slack_user_id = '' WHERE slack_user_id IS NULL;
UPDATE members SET telegram_chat_id = '' WHERE telegram_chat_id IS NULL;

ALTER TABLE members
    ALTER COLUMN slack_user_id SET DEFAULT '',
    ALTER COLUMN slack_user_id SET NOT NULL,
    ALTER COLUMN telegram_chat_id SET DEFAULT '',
    ALTER COLUMN telegram_chat_id SET NOT NULL;

CREATE UNIQUE INDEX idx_members_telegram_chat_id_unique
    ON members (telegram_chat_id)
    WHERE telegram_chat_id <> ''
      AND status NOT IN ('removed', 'purged', 'deactivated', 'suspended');
