-- SPDX-License-Identifier: AGPL-3.0-or-later

-- name: createKnowledgeEntry :one
INSERT INTO knowledge_entries (
    id, org_id, project_id, question, answer,
    summary, tags, entities, author_member_id, source, status
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id, org_id, project_id, question, answer,
          summary, tags, entities, author_member_id, source, status,
          created_at, updated_at;

-- name: knowledgeEntryByID :one
SELECT id, org_id, project_id, question, answer,
       summary, tags, entities, author_member_id, source, status,
       created_at, updated_at
FROM knowledge_entries
WHERE id = $1;

-- name: updateKnowledgeEntry :one
UPDATE knowledge_entries
SET question   = $2,
    answer     = $3,
    summary    = $4,
    tags       = $5,
    entities   = $6,
    status     = $7,
    updated_at = NOW()
WHERE id = $1
RETURNING id, org_id, project_id, question, answer,
          summary, tags, entities, author_member_id, source, status,
          created_at, updated_at;

-- name: markKnowledgeEntryStale :one
UPDATE knowledge_entries
SET status     = 'stale',
    updated_at = NOW()
WHERE id = $1
RETURNING id, org_id, project_id, question, answer,
          summary, tags, entities, author_member_id, source, status,
          created_at, updated_at;

-- name: activateKnowledgeEntry :one
-- Flip an entry to status='active' and bump updated_at ("last reviewed"). Shared
-- by ApproveKnowledgeEntry (an agent-suggested pending entry a human vetted) and
-- RevalidateKnowledgeEntry (a stale entry re-confirmed as current).
UPDATE knowledge_entries
SET status     = 'active',
    updated_at = NOW()
WHERE id = $1
RETURNING id, org_id, project_id, question, answer,
          summary, tags, entities, author_member_id, source, status,
          created_at, updated_at;

-- name: deleteKnowledgeEntry :exec
DELETE FROM knowledge_entries
WHERE id = $1;
