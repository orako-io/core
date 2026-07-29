-- SPDX-License-Identifier: AGPL-3.0-or-later

-- name: createAccount :one
INSERT INTO accounts (id, subject, email, display_name)
VALUES ($1, $2, $3, $4)
RETURNING id, subject, email, display_name, created_at, updated_at, password_hash, password_reset_version;

-- name: accountByID :one
SELECT id, subject, email, display_name, created_at, updated_at, password_hash, password_reset_version
FROM accounts
WHERE id = $1;

-- name: accountByEmail :one
SELECT id, subject, email, display_name, created_at, updated_at, password_hash, password_reset_version
FROM accounts
WHERE email = $1;

-- name: accountBySubject :one
SELECT id, subject, email, display_name, created_at, updated_at, password_hash, password_reset_version
FROM accounts
WHERE subject = $1;

-- name: setAccountPassword :exec
-- Sets (or clears) the bcrypt password hash for a local-auth account.
UPDATE accounts
SET password_hash = $2,
    password_reset_version = password_reset_version + 1,
    updated_at    = NOW()
WHERE id = $1;

-- name: accountCredentialByEmail :one
-- Returns the id + password hash for email+password login. password_hash is NULL
-- for IdP-only accounts (the caller must treat that as "no local password").
SELECT id, password_hash
FROM accounts
WHERE email = $1;

-- name: accountResetVersionByEmail :one
-- The current password_reset_version for a local account, keyed by email. Used to
-- stamp a reset token so it can be invalidated (single-use / on password change).
SELECT id, password_reset_version
FROM accounts
WHERE email = $1 AND password_hash IS NOT NULL;

-- name: resetAccountPassword :one
-- Atomically spends a reset-token version while replacing the password. A
-- concurrent replay, unknown account, or IdP-only account returns no row.
UPDATE accounts
SET password_hash = sqlc.arg(password_hash),
    password_reset_version = password_reset_version + 1,
    updated_at = NOW()
WHERE email = sqlc.arg(email)
  AND password_reset_version = sqlc.arg(expected_version)::bigint
  AND password_hash IS NOT NULL
RETURNING id;
