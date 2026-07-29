-- SPDX-License-Identifier: AGPL-3.0-or-later

-- name: createConversationSurface :execrows
-- ON CONFLICT DO NOTHING on (conversation_id, provider): a concurrent
-- creation (event replay) affects zero rows — the caller re-reads the winner.
INSERT INTO conversation_surfaces (id, conversation_id, provider, kind, channel_id, thread_id, covered_member_ids)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (conversation_id, provider) DO NOTHING;

-- name: surfaceByConversationProvider :one
SELECT id, conversation_id, provider, kind, channel_id, thread_id, covered_member_ids, created_at, archived_at
FROM conversation_surfaces
WHERE conversation_id = $1 AND provider = $2;

-- name: surfaceByThread :one
SELECT id, conversation_id, provider, kind, channel_id, thread_id, covered_member_ids, created_at, archived_at
FROM conversation_surfaces
WHERE provider = $1 AND thread_id = $2;

-- name: surfaceByChannelThread :one
-- Inbound correlation for Slack/Telegram: (thread_ts / message_id) is unique
-- only WITHIN a channel/chat, so the lookup keys on (provider, channel_id,
-- thread_id), not thread_id alone.
SELECT id, conversation_id, provider, kind, channel_id, thread_id, covered_member_ids, created_at, archived_at
FROM conversation_surfaces
WHERE provider = $1 AND channel_id = $2 AND thread_id = $3;

-- name: upsertCorrelationSurface :exec
-- Records/updates the direct-ask correlation surface a provider writes after an
-- outbound Deliver (Slack channel+thread_ts, Telegram chat+message_id). Last
-- writer wins on (conversation_id, provider) — a re-delivery overwrites the
-- channel/thread, matching the pre-surface flat-column UPDATE.
INSERT INTO conversation_surfaces (id, conversation_id, provider, kind, channel_id, thread_id, covered_member_ids)
VALUES ($1, $2, $3, 'thread', $4, $5, '{}')
ON CONFLICT (conversation_id, provider) DO UPDATE
SET channel_id = EXCLUDED.channel_id,
    thread_id  = EXCLUDED.thread_id;

-- name: addSurfaceCoveredMember :exec
-- Appends memberID to the covered set, idempotently (no duplicates).
UPDATE conversation_surfaces
SET covered_member_ids = ARRAY(SELECT DISTINCT unnest(covered_member_ids || $2::uuid))
WHERE id = $1;

-- name: markSurfaceArchived :execrows
-- CAS on archived_at so a duplicate CLOSED replay archives exactly once.
UPDATE conversation_surfaces
SET archived_at = NOW()
WHERE id = $1 AND archived_at IS NULL;

-- name: getChannelWebhook :one
SELECT channel_id, webhook_id, webhook_token
FROM discord_channel_webhooks
WHERE channel_id = $1;

-- name: upsertChannelWebhook :exec
-- Last writer wins: a webhook recreated after a manual deletion simply
-- replaces the stale cached pair.
INSERT INTO discord_channel_webhooks (channel_id, webhook_id, webhook_token)
VALUES ($1, $2, $3)
ON CONFLICT (channel_id) DO UPDATE
SET webhook_id = EXCLUDED.webhook_id, webhook_token = EXCLUDED.webhook_token;
