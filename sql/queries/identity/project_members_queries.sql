-- SPDX-License-Identifier: AGPL-3.0-or-later

-- name: addProjectMember :exec
INSERT INTO project_members (project_id, member_id, domains)
VALUES ($1, $2, $3);

-- name: addProjectMemberIdempotent :execrows
-- Insert-or-skip variant for the find-or-create invite path: a duplicate
-- membership must not abort the surrounding transaction, so the conflict is
-- absorbed in SQL and detected via the affected-row count (0 = already member).
INSERT INTO project_members (project_id, member_id, domains)
VALUES ($1, $2, $3)
ON CONFLICT (project_id, member_id) DO NOTHING;

-- name: updateProjectMemberDomains :execrows
UPDATE project_members
SET domains = $3
WHERE project_id = $1 AND member_id = $2;

-- name: setDomainsForMember :execrows
-- Self-serve expertise: replace a member's domains across ALL their project
-- memberships in one statement. Scope is the member (not one project), so an
-- onboarding member can set their own expertise without touching another
-- membership. Returns the number of memberships updated.
UPDATE project_members
SET domains = $2
WHERE member_id = $1;

-- name: projectMembersByProject :many
SELECT project_id, member_id, domains, created_at
FROM project_members
WHERE project_id = $1
ORDER BY created_at ASC;

-- name: projectMembersMissingSlackBinding :many
-- Members on the project with an email but no Slack binding yet — the backfill
-- set for Slack auto-bind (SyncChatDirectory).
SELECT m.id, m.email
FROM project_members pm
JOIN members m ON m.id = pm.member_id
WHERE pm.project_id = $1
  AND m.email IS NOT NULL AND m.email <> ''
  AND m.slack_user_id = ''
ORDER BY m.created_at ASC;
