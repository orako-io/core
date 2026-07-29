-- SPDX-License-Identifier: AGPL-3.0-or-later

-- name: createJoinToken :exec
-- Inserts a fresh join token. The caller (CreateJoinToken store method) revokes
-- any prior live token for the org in the same transaction first, so the partial
-- unique index (one live token per org) is never violated.
INSERT INTO org_join_tokens (token, org_id, project_id, created_by_member_id)
VALUES ($1, $2, $3, $4);

-- name: revokeLiveOrgJoinTokens :execrows
-- Revokes the org's live (non-revoked) token(s). Used both to rotate before a
-- fresh insert and by an explicit revoke. Idempotent: revokes nothing when the
-- org has no live token.
UPDATE org_join_tokens
SET revoked_at = NOW()
WHERE org_id = $1 AND revoked_at IS NULL;

-- name: joinTokenByToken :one
-- Resolves a token to its org/project and whether it has been revoked. The
-- org/project a redemption lands in come ONLY from this row.
SELECT org_id, project_id, (revoked_at IS NOT NULL)::bool AS revoked
FROM org_join_tokens
WHERE token = $1;

-- name: activeJoinTokenByOrg :one
-- The org's current live token, if any. adaptererr.ErrNotFound (no row) when the
-- org has never generated one or the last one was revoked.
SELECT token, org_id, project_id, created_at
FROM org_join_tokens
WHERE org_id = $1 AND revoked_at IS NULL
ORDER BY created_at DESC
LIMIT 1;
