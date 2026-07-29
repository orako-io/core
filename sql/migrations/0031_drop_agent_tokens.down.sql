-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Recreates agent_tokens as it stood right before the drop: the original
-- 0009 shape plus the project_ids scope column added by 0028.
CREATE TABLE agent_tokens (
    id           uuid PRIMARY KEY,
    member_id    uuid NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    name         text NOT NULL DEFAULT '',
    prefix       text NOT NULL,
    token_hash   bytea NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at   timestamptz,
    project_ids  text[] NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_agent_tokens_member ON agent_tokens (member_id, created_at DESC);
