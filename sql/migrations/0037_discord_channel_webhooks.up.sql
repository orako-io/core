-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Hub-and-spoke phase 5: identity mirroring. One Discord webhook per parent
-- channel (created lazily by the bot, cached here) lets mirrored messages
-- display the ORIGIN member's name ("Jordan · Slack") as the visible author
-- instead of the bot's own identity.
CREATE TABLE discord_channel_webhooks (
    channel_id    TEXT PRIMARY KEY,
    webhook_id    TEXT NOT NULL,
    webhook_token TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
