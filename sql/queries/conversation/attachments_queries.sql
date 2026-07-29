-- SPDX-License-Identifier: AGPL-3.0-or-later

-- name: createAttachment :exec
INSERT INTO attachments (
    id, project_id, conversation_id, message_id, uploaded_by_member_id,
    filename, mime_type, size_bytes, storage_key
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: attachmentByID :one
SELECT id, project_id, conversation_id, message_id, uploaded_by_member_id,
       filename, mime_type, size_bytes, storage_key, created_at
FROM attachments
WHERE id = $1;

-- name: linkAttachmentsToMessage :execrows
-- Links a set of previously-uploaded, still-unlinked attachments to a message,
-- scoped to the conversation so an id from another conversation can't be
-- grafted on. Returns the rows affected so the caller can detect a mismatch.
UPDATE attachments
SET message_id = $1
WHERE conversation_id = $2
  AND message_id IS NULL
  AND id = ANY($3::uuid[]);

-- name: attachmentsByConversation :many
-- All linked attachments for a conversation, for the join into message reads.
SELECT id, project_id, conversation_id, message_id, uploaded_by_member_id,
       filename, mime_type, size_bytes, storage_key, created_at
FROM attachments
WHERE conversation_id = $1 AND message_id IS NOT NULL
ORDER BY created_at ASC;

-- name: storageKeysByConversation :many
-- Blob keys to delete when a conversation is hard-deleted (the rows cascade;
-- the blobs are removed best-effort by the delete handler).
SELECT storage_key
FROM attachments
WHERE conversation_id = $1;
