-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0054_slack_telegram_surfaces: kill the split-brain in inbound reply
-- correlation. Discord already correlates inbound via conversation_surfaces;
-- Slack and Telegram used four flat columns on conversations
-- (slack_channel_id/slack_thread_ts, telegram_chat_id/telegram_message_id).
-- conversation_surfaces already carries BOTH channel_id and thread_id, so
-- Slack/Telegram map onto it natively:
--
--   provider   | channel_id        | thread_id
--   ---------- | ----------------- | ------------------
--   slack      | slack_channel_id  | slack_thread_ts
--   telegram   | telegram_chat_id  | telegram_message_id
--
-- All three providers now read inbound correlation from one table.

-- 1. Widen the 0053 provider CHECK to admit slack/telegram. kind stays 'thread'
--    (the (channel_id, thread_id) pair IS the reply anchor), so the kind CHECK
--    is untouched.
ALTER TABLE conversation_surfaces DROP CONSTRAINT conversation_surfaces_provider_check;
ALTER TABLE conversation_surfaces
    ADD CONSTRAINT conversation_surfaces_provider_check
    CHECK (provider IN ('discord', 'slack', 'telegram'));

-- 2. Re-key the uniqueness BEFORE the backfill. The old (provider, thread_id)
--    UNIQUE was global: it fits Discord (thread_id is a globally-unique
--    snowflake) but NOT Slack/Telegram, whose thread_ts / message_id repeat
--    across channels/chats (two DMs can both hold message_id 47). Re-scope it to
--    Discord as a partial unique index — preserving the exact invariant
--    surfaceByThread() relies on — and add the channel-scoped
--    (provider, channel_id, thread_id) unique that backs surfaceByChannelThread()
--    for every provider. This must happen first so the backfill inserts against
--    the correct constraints.
ALTER TABLE conversation_surfaces DROP CONSTRAINT uq_conversation_surfaces_provider_thread;
CREATE UNIQUE INDEX uq_conversation_surfaces_discord_thread
    ON conversation_surfaces (provider, thread_id)
    WHERE provider = 'discord';
CREATE UNIQUE INDEX uq_conversation_surfaces_provider_channel_thread
    ON conversation_surfaces (provider, channel_id, thread_id);

-- 3. Backfill: one surface per correlated conversation. TextOrNull stored the
--    "unset" binding as NULL, so a set binding is non-NULL non-empty; `<> ''`
--    matches exactly those (NULL <> '' is NULL → excluded). ON CONFLICT DO
--    NOTHING keeps the move idempotent and tolerates a pre-existing duplicate
--    correlation (two conversations flat-bound to the same channel+thread): the
--    flat-column lookup already resolved such a collision to one arbitrary row,
--    so collapsing it to one surface preserves behavior. A clean dataset (one
--    thread ⇒ one conversation) is an exact 1:1 move.
INSERT INTO conversation_surfaces
    (id, conversation_id, provider, kind, channel_id, thread_id, covered_member_ids)
SELECT gen_random_uuid(), id, 'slack', 'thread', slack_channel_id, slack_thread_ts, '{}'
FROM conversations
WHERE slack_channel_id <> '' AND slack_thread_ts <> ''
ON CONFLICT (provider, channel_id, thread_id) DO NOTHING;

INSERT INTO conversation_surfaces
    (id, conversation_id, provider, kind, channel_id, thread_id, covered_member_ids)
SELECT gen_random_uuid(), id, 'telegram', 'thread', telegram_chat_id, telegram_message_id, '{}'
FROM conversations
WHERE telegram_chat_id <> '' AND telegram_message_id <> ''
ON CONFLICT (provider, channel_id, thread_id) DO NOTHING;

-- 4. Drop the now-unused flat columns (their two composite indexes fall with
--    them).
DROP INDEX IF EXISTS idx_conversations_slack_thread;
DROP INDEX IF EXISTS idx_conversations_telegram;
ALTER TABLE conversations
    DROP COLUMN slack_channel_id,
    DROP COLUMN slack_thread_ts,
    DROP COLUMN telegram_chat_id,
    DROP COLUMN telegram_message_id;
