-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0003_project_providers: per-project messaging provider credentials stored
-- after a successful OAuth install. Credentials are currently stored as
-- plaintext JSONB; a production deployment must encrypt them at rest
-- (e.g. envelope encryption via a KMS) before GA.
--
-- One row per (project, kind); kind is 'slack' in v1.
-- The credentials column schema for Slack:
--   { "bot_token": "xoxb-...", "team_id": "T...", "team_name": "...", "app_id": "A..." }

CREATE TABLE project_providers (
    id          UUID PRIMARY KEY,
    project_id  UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,    -- 'slack' | 'teams' | 'telegram'
    credentials JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, kind)
);

CREATE INDEX idx_project_providers_project ON project_providers (project_id);
