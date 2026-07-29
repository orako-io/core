-- SPDX-License-Identifier: AGPL-3.0-or-later

-- name: addOrgMember :exec
INSERT INTO org_members (org_id, account_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (org_id, account_id) DO UPDATE
    SET role = EXCLUDED.role;

-- name: orgMemberRole :one
SELECT role
FROM org_members
WHERE org_id = $1 AND account_id = $2
LIMIT 1;

-- name: countOrgAdmins :one
SELECT COUNT(*) FROM org_members WHERE org_id = $1 AND role = 'admin';

-- name: countInstanceSeats :one
-- Instance-wide seat count for a licensed self-host: distinct billable persons
-- (accounts that are a member of any org). Backs the Licensed edition seat cap
-- (1 instance = 1 customer = N seats, all orgs combined).
SELECT COUNT(DISTINCT account_id) FROM org_members;
