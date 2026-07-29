-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Down: re-introduce the project-scoped role column and restore the composite
-- primary key. The original per-member roles cannot be recovered (they were
-- collapsed in the up migration), so every surviving row is reseeded to 'dev';
-- admin authority is not restored here and remains sourced from org_members.

ALTER TABLE project_members
    ADD COLUMN role TEXT NOT NULL DEFAULT 'dev';

ALTER TABLE project_members
    ALTER COLUMN role DROP DEFAULT;

ALTER TABLE project_members DROP CONSTRAINT project_members_pkey;

ALTER TABLE project_members
    ADD CONSTRAINT project_members_pkey PRIMARY KEY (project_id, member_id, role);
