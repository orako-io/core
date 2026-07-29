-- SPDX-License-Identifier: AGPL-3.0-or-later

-- name: createProject :one
INSERT INTO projects (id, name)
VALUES ($1, $2)
RETURNING id, name, created_at, updated_at;

-- name: createProjectInOrg :exec
-- Creates a project that is already attached to an organization. org_id must not
-- be nil; the handler validates this before calling the store.
INSERT INTO projects (id, name, org_id)
VALUES ($1, $2, $3);

-- name: projectByID :one
SELECT id, name, org_id, archived_at, created_at, updated_at
FROM projects
WHERE id = $1;

-- name: countProjectsByOrg :one
-- Projects owned by an org. Backs the Community-edition 1-project cap.
SELECT COUNT(*) FROM projects
WHERE org_id = $1;
