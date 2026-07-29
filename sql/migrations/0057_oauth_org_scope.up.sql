-- SPDX-License-Identifier: AGPL-3.0-or-later

ALTER TABLE oauth_authorization_codes
    ADD COLUMN org_id uuid REFERENCES organizations(id) ON DELETE CASCADE;

ALTER TABLE oauth_tokens
    ADD COLUMN org_id uuid REFERENCES organizations(id) ON DELETE CASCADE;

UPDATE oauth_authorization_codes ac
SET org_id = (
    SELECT p.org_id
    FROM project_members pm
    JOIN projects p ON p.id = pm.project_id
    WHERE pm.member_id = ac.member_id
    ORDER BY
        CASE WHEN p.id::text = ANY(ac.project_ids) THEN 0 ELSE 1 END,
        p.created_at,
        p.id
    LIMIT 1
);

UPDATE oauth_tokens ot
SET org_id = (
    SELECT p.org_id
    FROM project_members pm
    JOIN projects p ON p.id = pm.project_id
    WHERE pm.member_id = ot.member_id
    ORDER BY
        CASE WHEN p.id::text = ANY(ot.project_ids) THEN 0 ELSE 1 END,
        p.created_at,
        p.id
    LIMIT 1
);

DELETE FROM oauth_tokens
WHERE client_id = 'mcp_client_machine_tokens'
  AND org_id IS NULL;

ALTER TABLE oauth_tokens
    ADD CONSTRAINT oauth_machine_token_org_required
    CHECK (client_id <> 'mcp_client_machine_tokens' OR org_id IS NOT NULL);

CREATE INDEX idx_oauth_authorization_codes_org ON oauth_authorization_codes (org_id, expires_at);
CREATE INDEX idx_oauth_tokens_org ON oauth_tokens (org_id, created_at DESC);
