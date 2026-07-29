-- SPDX-License-Identifier: AGPL-3.0-or-later

-- name: getInstanceLicense :one
-- The single stored license row, if any. adaptererr.ErrNotFound (no row) when no
-- key has ever been set — the fail-safe boot path treats that miss as Community.
SELECT license_key, set_by, set_at
FROM instance_license
WHERE singleton = TRUE;

-- name: upsertInstanceLicense :exec
-- Stores (or replaces) the one license row. The singleton PRIMARY KEY makes this
-- an upsert against the single row: a fresh paste, a Replace, and a refresh-loop
-- renewal all land here.
INSERT INTO instance_license (singleton, license_key, set_by, set_at)
VALUES (TRUE, $1, $2, NOW())
ON CONFLICT (singleton)
DO UPDATE SET license_key = EXCLUDED.license_key, set_by = EXCLUDED.set_by, set_at = NOW();

-- name: deleteInstanceLicense :exec
-- Clears the stored license (revert to Community). Idempotent: a clear with no
-- row is a no-op, not an error.
DELETE FROM instance_license WHERE singleton = TRUE;
