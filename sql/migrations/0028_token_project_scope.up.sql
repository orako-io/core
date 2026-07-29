-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Scope agent connections to a chosen set of projects. project_ids is an
-- ordered array of project UUIDs (as text[], matching the redirect_uris
-- house style and sidestepping the pgx uuid-array codec); the first element
-- is the primary project used when a tool omits project_id. An empty array
-- means "all the caller's projects" — the legacy, unscoped behavior, so the
-- default keeps every existing token working exactly as before.

ALTER TABLE oauth_authorization_codes
    ADD COLUMN project_ids text[] NOT NULL DEFAULT '{}';

ALTER TABLE oauth_tokens
    ADD COLUMN project_ids text[] NOT NULL DEFAULT '{}';

ALTER TABLE agent_tokens
    ADD COLUMN project_ids text[] NOT NULL DEFAULT '{}';
