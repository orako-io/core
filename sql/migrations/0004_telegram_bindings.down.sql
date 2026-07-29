-- SPDX-License-Identifier: AGPL-3.0-or-later

DROP INDEX IF EXISTS idx_conversations_telegram;

ALTER TABLE conversations
    DROP COLUMN IF EXISTS telegram_message_id,
    DROP COLUMN IF EXISTS telegram_chat_id;

ALTER TABLE members
    DROP COLUMN IF EXISTS telegram_chat_id;
