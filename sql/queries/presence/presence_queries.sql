-- SPDX-License-Identifier: AGPL-3.0-or-later

-- name: upsertPresence :one
INSERT INTO presence (member_id, online, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (member_id)
DO UPDATE SET online = EXCLUDED.online, updated_at = NOW()
RETURNING member_id, online, updated_at;

-- name: presenceByMember :one
SELECT member_id, online, updated_at
FROM presence
WHERE member_id = $1;
