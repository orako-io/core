-- SPDX-License-Identifier: AGPL-3.0-or-later

-- name: createOrganization :one
INSERT INTO organizations (id, name)
VALUES ($1, $2)
RETURNING id, name, created_at, updated_at;

-- name: organizationByID :one
SELECT id, name, created_at, updated_at
FROM organizations
WHERE id = $1;

-- name: updateOrganization :one
UPDATE organizations
SET name       = $2,
    updated_at = NOW()
WHERE id = $1
RETURNING id, name, created_at, updated_at;

-- name: countOrganizations :one
-- Total organizations in the instance. Backs the Community-edition 1-org cap.
SELECT COUNT(*) FROM organizations;
