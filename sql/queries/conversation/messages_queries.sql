-- SPDX-License-Identifier: AGPL-3.0-or-later

-- name: addMessage :one
INSERT INTO messages (id, conversation_id, author_member_id, role, body, source, agent_client)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, conversation_id, author_member_id, role, body, created_at, source, agent_client;

-- name: messagesByConversation :many
SELECT id, conversation_id, author_member_id, role, body, created_at, source, agent_client
FROM messages
WHERE conversation_id = $1
ORDER BY created_at ASC;

-- name: memberNamesByIDs :many
SELECT id, display_name
FROM members
WHERE id = ANY($1::uuid[]);
