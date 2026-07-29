-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0007_drop_project_member_role: retire the project-scoped role from
-- project_members. Admin authority now comes solely from org_members.role='admin'
-- (org-scoped RBAC); a member's function within a project is modelled by domains
-- (expertise tags) going forward. This migration:
--   1. Collapses any multi-role member into a single row per (project_id,
--      member_id), unioning their domain tags so no expertise is lost.
--   2. Drops the role column.
--   3. Repoints the primary key to (project_id, member_id).

-- 1. Snapshot one merged row per (project_id, member_id). A member who held
--    several roles currently has several rows; after the PK change those would
--    collide, so merge them first. Domains are unioned across the member's rows;
--    created_at keeps the earliest join time.
CREATE TEMP TABLE pm_merged AS
SELECT project_id, member_id, MIN(created_at) AS created_at
FROM project_members
GROUP BY project_id, member_id;

ALTER TABLE pm_merged ADD COLUMN domains TEXT[];

UPDATE pm_merged m
SET domains = COALESCE((
    SELECT array_agg(DISTINCT d)
    FROM project_members pm, unnest(pm.domains) AS d
    WHERE pm.project_id = m.project_id AND pm.member_id = m.member_id
), '{}');

-- 2. Rebuild project_members without role, keyed by (project_id, member_id).
DELETE FROM project_members;

ALTER TABLE project_members DROP CONSTRAINT project_members_pkey;
ALTER TABLE project_members DROP COLUMN role;

INSERT INTO project_members (project_id, member_id, domains, created_at)
SELECT project_id, member_id, domains, created_at
FROM pm_merged;

ALTER TABLE project_members
    ADD CONSTRAINT project_members_pkey PRIMARY KEY (project_id, member_id);

DROP TABLE pm_merged;
