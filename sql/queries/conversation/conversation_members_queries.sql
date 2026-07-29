-- SPDX-License-Identifier: AGPL-3.0-or-later

-- conversation_members is the single membership table: one row per
-- (conversation, member), carrying how they joined. invited_at marks a
-- dispatched pool candidate (ex-conversation_candidates); added_at marks an
-- explicitly added participant (ex-conversation_participants). A member can be
-- both — one row, both column sets.
--
--   active candidate     = invited_at IS NOT NULL AND excluded_at IS NULL
--   explicit participant = added_at   IS NOT NULL

-- name: addConversationCandidate :exec
-- Dispatch: add member to the pool, idempotently. A re-dispatch keeps the
-- original invited_at (COALESCE), and never clears an existing exclusion.
INSERT INTO conversation_members (conversation_id, member_id, invited_at)
VALUES ($1, $2, NOW())
ON CONFLICT (conversation_id, member_id)
DO UPDATE SET invited_at = COALESCE(conversation_members.invited_at, NOW());

-- name: candidatesByConversation :many
-- All pool candidates (dispatched), excluded ones included. Pure participants
-- (invited_at IS NULL) are not candidates.
SELECT member_id, invited_at, excluded_at
FROM conversation_members
WHERE conversation_id = $1 AND invited_at IS NOT NULL
ORDER BY invited_at ASC;

-- name: excludeConversationCandidate :exec
-- Release a candidate from the pool. Scoped to rows that were actually
-- dispatched so a pure participant row is never stamped excluded.
UPDATE conversation_members
SET excluded_at = NOW()
WHERE conversation_id = $1 AND member_id = $2 AND invited_at IS NOT NULL;

-- name: isActiveCandidate :one
-- Still an active (dispatched, not excluded) pool candidate — i.e. on the
-- conversation's distribution list as a candidate, not merely a participant.
SELECT EXISTS(
  SELECT 1 FROM conversation_members
  WHERE conversation_id = $1 AND member_id = $2
    AND invited_at IS NOT NULL AND excluded_at IS NULL
) AS active;

-- name: recordFirstResponder :execrows
-- Records who first answered, for KB attribution: a compare-and-set so only the
-- FIRST answerer is written (first writer wins; a later answer affects zero
-- rows). Not a claim and not a gate — anyone on the conversation can still
-- reply; this only remembers the first responder.
UPDATE conversations
SET responder_member_id = $2,
    updated_at           = NOW()
WHERE id = $1 AND responder_member_id IS NULL;

-- name: openPoolConversationsForMember :many
-- Unanswered pool conversations the member was contacted on (inbox "awaiting
-- an answer"). Only active candidacies count — a pure participant row does not
-- put a conversation in the member's pool inbox.
SELECT c.id, c.project_id, c.asker_member_id, c.responder_member_id,
       c.status, c.question, c.title, c.context,
       c.summary, c.tags, c.entities,
       c.created_at, c.updated_at
FROM conversations c
JOIN conversation_members cc ON cc.conversation_id = c.id
WHERE cc.member_id = $1
  AND cc.invited_at IS NOT NULL
  AND cc.excluded_at IS NULL
  AND c.responder_member_id IS NULL
  AND c.status = 'open'
ORDER BY c.created_at DESC;

-- name: projectMembersByDomains :many
-- Candidate resolution at dispatch: active project members whose expertise
-- intersects the requested domains, excluding the asker.
SELECT pm.member_id
FROM project_members pm
JOIN members m ON m.id = pm.member_id
WHERE pm.project_id = $1
  AND m.status = 'active'
  AND pm.domains && $2::text[]
  AND pm.member_id <> $3;

-- name: addParticipant :exec
-- Explicitly add member to the thread, idempotently. A re-add keeps the
-- original added_at/added_by (COALESCE); a member already in the pool as a
-- candidate gets added_at stamped on the same row (becomes both).
INSERT INTO conversation_members (conversation_id, member_id, added_at, added_by)
VALUES ($1, $2, NOW(), $3)
ON CONFLICT (conversation_id, member_id)
DO UPDATE SET added_at = COALESCE(conversation_members.added_at, NOW()),
              added_by = COALESCE(conversation_members.added_by, EXCLUDED.added_by);

-- name: participantsByConversation :many
-- Explicitly-added participants only (the asker and assigned responder are
-- implicit and not stored here).
SELECT conversation_id, member_id, added_by, added_at
FROM conversation_members
WHERE conversation_id = $1 AND added_at IS NOT NULL
ORDER BY added_at ASC;

-- name: participantsByConversations :many
SELECT conversation_id, member_id, added_by, added_at
FROM conversation_members
WHERE conversation_id = ANY($1::uuid[]) AND added_at IS NOT NULL
ORDER BY added_at ASC;

-- name: activeCandidatesByConversations :many
-- Still-active pool candidates (dispatched, not excluded) for a batch of
-- conversations.
SELECT conversation_id, member_id
FROM conversation_members
WHERE conversation_id = ANY($1::uuid[])
  AND invited_at IS NOT NULL
  AND excluded_at IS NULL
ORDER BY invited_at ASC;
