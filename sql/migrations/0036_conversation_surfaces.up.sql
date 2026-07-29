-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Hub-and-spoke phase 4: a conversation is one durable hub with at most one
-- native SURFACE per platform — for Discord, a private thread in the
-- project's channel where guild participants discuss natively; the per-member
-- DM stays the universal fallback. covered_member_ids records which members
-- the surface reaches (successfully invited to the thread): they are excluded
-- from the DM fan-out for this conversation.
CREATE TABLE conversation_surfaces (
    id                 UUID PRIMARY KEY,
    conversation_id    UUID NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    provider           TEXT NOT NULL,              -- 'discord' (slack/teams later)
    kind               TEXT NOT NULL,              -- 'thread' (a 'dm' kind may come later)
    channel_id         TEXT NOT NULL,              -- parent channel the thread lives in
    thread_id          TEXT NOT NULL,              -- the thread itself (inbound correlation key)
    covered_member_ids UUID[] NOT NULL DEFAULT '{}',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at        TIMESTAMPTZ,

    -- Max one surface per platform per conversation; a thread maps back to
    -- exactly one conversation.
    CONSTRAINT uq_conversation_surfaces_conv_provider UNIQUE (conversation_id, provider),
    CONSTRAINT uq_conversation_surfaces_provider_thread UNIQUE (provider, thread_id)
);

CREATE INDEX idx_conversation_surfaces_thread ON conversation_surfaces (provider, thread_id);
