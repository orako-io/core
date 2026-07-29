-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Attachments: files/images carried by conversation messages. The bytes live
-- in blob storage (S3-compatible); this table is the metadata + storage key.
-- message_id is nullable so an agent can upload BEFORE posting the message
-- that references it (upload → get id → follow_up with attachment_ids); the
-- link is set when the message is posted. Deleting a conversation cascades
-- here; the blobs are deleted best-effort by the delete handler.
CREATE TABLE attachments (
    id                     UUID PRIMARY KEY,
    project_id             UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    conversation_id        UUID NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    message_id             UUID REFERENCES messages (id) ON DELETE CASCADE,
    uploaded_by_member_id  UUID REFERENCES members (id) ON DELETE SET NULL,
    filename               TEXT NOT NULL,
    mime_type              TEXT NOT NULL,
    size_bytes             BIGINT NOT NULL,
    storage_key            TEXT NOT NULL UNIQUE,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_attachments_conversation ON attachments (conversation_id);
CREATE INDEX idx_attachments_message ON attachments (message_id) WHERE message_id IS NOT NULL;
