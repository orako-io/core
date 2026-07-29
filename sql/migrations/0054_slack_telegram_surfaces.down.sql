-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Faithful inverse of 0054: restore the four flat correlation columns on
-- conversations, copy the Slack/Telegram surface rows back into them, drop those
-- surface rows and the channel-scoped index, and restore the global
-- (provider, thread_id) UNIQUE and the discord-only provider CHECK.

-- 1. Re-add the flat columns (NULLable TEXT, their original shape) and their
--    composite lookup indexes.
ALTER TABLE conversations
    ADD COLUMN slack_channel_id    TEXT,
    ADD COLUMN slack_thread_ts     TEXT,
    ADD COLUMN telegram_chat_id    TEXT,
    ADD COLUMN telegram_message_id TEXT;

CREATE INDEX idx_conversations_slack_thread ON conversations (slack_channel_id, slack_thread_ts);
CREATE INDEX idx_conversations_telegram ON conversations (telegram_chat_id, telegram_message_id);

-- 2. Copy the correlation surfaces back onto the conversation rows.
UPDATE conversations c
SET slack_channel_id = s.channel_id,
    slack_thread_ts  = s.thread_id
FROM conversation_surfaces s
WHERE s.conversation_id = c.id AND s.provider = 'slack';

UPDATE conversations c
SET telegram_chat_id    = s.channel_id,
    telegram_message_id = s.thread_id
FROM conversation_surfaces s
WHERE s.conversation_id = c.id AND s.provider = 'telegram';

-- 3. Remove the Slack/Telegram surface rows and the channel-scoped index.
DELETE FROM conversation_surfaces WHERE provider IN ('slack', 'telegram');
DROP INDEX IF EXISTS uq_conversation_surfaces_provider_channel_thread;

-- 4. Restore the global (provider, thread_id) UNIQUE (only Discord rows remain,
--    so it re-adds cleanly) and drop the discord-only partial index.
DROP INDEX IF EXISTS uq_conversation_surfaces_discord_thread;
ALTER TABLE conversation_surfaces
    ADD CONSTRAINT uq_conversation_surfaces_provider_thread UNIQUE (provider, thread_id);

-- 5. Restore the discord-only provider CHECK.
ALTER TABLE conversation_surfaces DROP CONSTRAINT conversation_surfaces_provider_check;
ALTER TABLE conversation_surfaces
    ADD CONSTRAINT conversation_surfaces_provider_check
    CHECK (provider IN ('discord'));
