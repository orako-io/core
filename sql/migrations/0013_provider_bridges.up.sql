-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0013_provider_bridges: the delivery ledger for pool DM fan-out, plus the
-- schema surface the bridge needs — Teams/Discord bindings, per-member
-- binding-error surfacing, and the alert-channel escalation rung.
--
-- provider_messages — one row per (conversation, member) delivery: the
--   channel + message ref returned by Provider.Deliver, so a later call can
--   edit that specific message (claim/release/closure projection, phase 3) or
--   correlate an inbound reply back to its conversation. The existing
--   conversations.slack_channel_id/thread_ts columns stay as-is: they serve
--   the 1:1 direct-ask path, which never fans out to more than one message.

CREATE TABLE provider_messages (
    id              UUID PRIMARY KEY,
    conversation_id UUID NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    member_id       UUID NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    provider_kind   TEXT NOT NULL,
    channel_id      TEXT NOT NULL,
    message_ref     TEXT NOT NULL,
    state           TEXT NOT NULL
        CHECK (state IN ('posted', 'claimed_won', 'claimed_lost', 'released', 'resolved', 'failed')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (conversation_id, member_id)
);

CREATE INDEX idx_provider_messages_conversation ON provider_messages (conversation_id);
-- Inbound correlation: a reply's (channel, thread ref) resolves back to the
-- conversation/member pair when the direct conversations-thread lookup misses.
CREATE INDEX idx_provider_messages_channel_ref ON provider_messages (channel_id, message_ref);

-- members: Teams/Discord bindings (mirrors slack_user_id/telegram_chat_id) and
-- the last delivery failure on the member's bound channel, surfaced on the
-- member card so a broken binding is visible instead of a silent black hole.
ALTER TABLE members
    ADD COLUMN teams_user_id   TEXT NOT NULL DEFAULT '',
    ADD COLUMN discord_user_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN binding_error   TEXT NOT NULL DEFAULT '';

ALTER TABLE members DROP CONSTRAINT members_delivery_channel_check;
ALTER TABLE members
    ADD CONSTRAINT members_delivery_channel_check
        CHECK (delivery_channel IN ('slack', 'teams', 'telegram', 'discord', 'dashboard'));

-- organizations: the third escalation rung (channel alert) and its org-wide
-- fallback channel. NULL = product default; 0 = disabled for the org.
ALTER TABLE organizations
    ADD COLUMN alert_timeout_seconds     BIGINT,
    ADD COLUMN default_alert_channel_id TEXT NOT NULL DEFAULT '';

-- project_providers: the project-scoped alert channel, always tried first;
-- the org default is the fallback when this is empty.
ALTER TABLE project_providers
    ADD COLUMN alert_channel_id TEXT NOT NULL DEFAULT '';
