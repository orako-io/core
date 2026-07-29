-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0023_org_providers: messaging provider credentials keyed by organization.
--
-- Orako DMs people (org-level identities), and one Slack/Teams workspace serves
-- all of an org's projects, so the provider *connection* belongs to the org,
-- not the project. This table holds the credentials; the per-project alert
-- channel stays on project_providers.alert_channel_ids as an override.
--
-- One row per (org, kind). Credentials are plaintext JSONB for now (same caveat
-- as project_providers: encrypt at rest before GA).

CREATE TABLE org_providers (
    id          UUID PRIMARY KEY,
    org_id      UUID NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,    -- 'slack' | 'teams' | 'discord' | 'telegram'
    credentials JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, kind)
);

CREATE INDEX idx_org_providers_org ON org_providers (org_id);

-- Backfill: collapse existing per-project credentials to one row per (org, kind).
-- Keeps the most recently updated project's credentials for each (org, kind).
-- No-op where there are no configured providers (e.g. a freshly purged prod).
INSERT INTO org_providers (id, org_id, kind, credentials)
SELECT DISTINCT ON (p.org_id, pp.kind)
       gen_random_uuid(), p.org_id, pp.kind, pp.credentials
FROM project_providers pp
JOIN projects p ON p.id = pp.project_id
WHERE p.org_id IS NOT NULL
ORDER BY p.org_id, pp.kind, pp.updated_at DESC
ON CONFLICT (org_id, kind) DO NOTHING;
