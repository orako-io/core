-- SPDX-License-Identifier: AGPL-3.0-or-later

ALTER TABLE members DROP COLUMN account_id;
ALTER TABLE projects DROP COLUMN org_id;
DROP TABLE org_members;
DROP TABLE organizations;
DROP TABLE accounts;
