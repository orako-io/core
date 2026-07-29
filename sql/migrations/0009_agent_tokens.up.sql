-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Personal agent tokens: durable CLI/MCP credentials. The secret never lands
-- here — only its SHA-256. No expiry column by design: revoked_at is the only
-- invalidation.
CREATE TABLE agent_tokens (
    id           uuid PRIMARY KEY,
    member_id    uuid NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    name         text NOT NULL DEFAULT '',
    prefix       text NOT NULL,
    token_hash   bytea NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at   timestamptz
);

CREATE INDEX idx_agent_tokens_member ON agent_tokens (member_id, created_at DESC);
