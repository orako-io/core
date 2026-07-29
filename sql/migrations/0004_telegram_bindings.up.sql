-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0004_telegram_bindings: binds Orako members and conversations to Telegram
-- identities. Telegram has no thread model; correlation is via reply_to_message.
--
-- members.telegram_chat_id       — the Telegram user/chat ID of the specialist.
--                                  Bound manually by an admin at onboarding time.
-- conversations.telegram_chat_id    — the Telegram chat ID the bot messaged into.
-- conversations.telegram_message_id — the bot's outbound message_id that the
--                                     specialist must reply_to for inbound correlation.

ALTER TABLE members
    ADD COLUMN telegram_chat_id TEXT;

ALTER TABLE conversations
    ADD COLUMN telegram_chat_id TEXT,
    ADD COLUMN telegram_message_id TEXT;

CREATE INDEX idx_conversations_telegram ON conversations (telegram_chat_id, telegram_message_id);
