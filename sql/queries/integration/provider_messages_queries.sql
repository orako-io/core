-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The delivery ledger: one row per (conversation, member) pool DM, keyed for
-- idempotent re-delivery and inbound correlation by (channel, message ref).

-- name: upsertProviderMessage :execrows
-- ON CONFLICT DO NOTHING, not DO UPDATE: the first successfully-recorded
-- delivery for a (conversation, member) pair must never be overwritten by a
-- later call — a replayed CONVERSATION_OPENED (at-least-once redelivery)
-- would otherwise regress a row already at claimed_won/claimed_lost/released/
-- resolved back to "posted", re-arming the projector's state guards and
-- letting a second claim/release cycle run. The application layer
-- (delivery_notifier.go) already skips re-delivering to a candidate with an
-- existing row; this is the defense-in-depth backstop, and it must be a
-- pure no-op on conflict, never a write.
INSERT INTO provider_messages (id, conversation_id, member_id, provider_kind, channel_id, message_ref, state)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (conversation_id, member_id) DO NOTHING;

-- name: providerMessagesByConversation :many
SELECT id, conversation_id, member_id, provider_kind, channel_id, message_ref, state, created_at, updated_at
FROM provider_messages
WHERE conversation_id = $1
ORDER BY created_at ASC;

-- name: providerMessageByChannelRef :one
-- Inbound correlation: a reply's (channel, message ref) resolves back to its
-- conversation/member pair when the direct conversations-thread lookup misses
-- (the pool path never writes conversations.slack_thread_ts).
SELECT id, conversation_id, member_id, provider_kind, channel_id, message_ref, state, created_at, updated_at
FROM provider_messages
WHERE channel_id = $1 AND message_ref = $2
ORDER BY created_at DESC
LIMIT 1;

-- name: setProviderMessageState :execrows
UPDATE provider_messages
SET state = $2, updated_at = NOW()
WHERE id = $1;

-- name: finalizeProviderMessage :execrows
-- Closes the reserve→deliver→finalize window for the pool fan-out: a row
-- written as 'reserving' (no channel/ref yet, before Deliver ever ran) is
-- finalized here with the channel/ref Deliver actually returned, in the same
-- write as its state transition (normally to 'posted'). Guarded on the row
-- still being 'reserving' so this can never regress a row a concurrent path
-- already finalized or advanced past.
UPDATE provider_messages
SET channel_id = $2, message_ref = $3, state = $4, updated_at = NOW()
WHERE id = $1 AND state = 'reserving';

-- name: providerMessageLatestByChannel :one
-- Inbound correlation for providers whose conversation model has no stable
-- per-message reference to key an exact (channel, ref) match on (Microsoft
-- Teams personal chats: every message in the 1:1 conversation shares the same
-- conversation id, with no thread/reply id the client reliably sets). Picks
-- the most recently updated still-open row for the channel; a member with two
-- concurrent unclaimed pool questions on the same channel is a known,
-- accepted ambiguity (the newer one wins the reply).
SELECT id, conversation_id, member_id, provider_kind, channel_id, message_ref, state, created_at, updated_at
FROM provider_messages
WHERE channel_id = $1 AND state IN ('posted', 'claimed_won')
ORDER BY updated_at DESC
LIMIT 1;
