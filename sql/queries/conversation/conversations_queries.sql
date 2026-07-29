-- SPDX-License-Identifier: AGPL-3.0-or-later

-- name: createConversation :one
INSERT INTO conversations (
    id, project_id, asker_member_id, responder_member_id,
    status, question, title, context,
    summary, tags, entities
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id, project_id, asker_member_id, responder_member_id,
          status, question, title, context,
          summary, tags, entities,
          created_at, updated_at;

-- name: conversationByID :one
SELECT id, project_id, asker_member_id, responder_member_id,
       status, question, title, context,
       summary, tags, entities,
       created_at, updated_at
FROM conversations
WHERE id = $1;

-- name: updateConversationStatus :one
UPDATE conversations
SET status     = $2,
    updated_at = NOW()
WHERE id = $1
RETURNING id, project_id, asker_member_id, responder_member_id,
          status, question, title, context,
          summary, tags, entities,
          created_at, updated_at;

-- name: updateConversationMetadata :one
UPDATE conversations
SET summary    = $2,
    tags       = $3,
    entities   = $4,
    updated_at = NOW()
WHERE id = $1
RETURNING id, project_id, asker_member_id, responder_member_id,
          status, question, title, context,
          summary, tags, entities,
          created_at, updated_at;
