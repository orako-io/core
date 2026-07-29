-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Shareable org join codes. A person redeems one at the MCP OAuth consent
-- screen to self-provision as a *pending* member of the token's org/project
-- (can connect + ask, not routable until an admin approves). The org and
-- project a redemption lands in come ONLY from the token row, never from
-- caller input. Tokens are reusable and revocable, with no expiry: an admin
-- regenerates to rotate. The partial unique index enforces at most one live
-- (non-revoked) code per org.
CREATE TABLE org_join_tokens (
    token                TEXT PRIMARY KEY,
    org_id               UUID NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id           UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    created_by_member_id UUID,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at           TIMESTAMPTZ
);

CREATE UNIQUE INDEX org_join_tokens_one_live_per_org
    ON org_join_tokens (org_id)
    WHERE revoked_at IS NULL;
