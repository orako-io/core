-- SPDX-License-Identifier: AGPL-3.0-or-later

-- name: createMember :one
INSERT INTO members (id, email, display_name, slack_user_id, telegram_chat_id, delivery_channel, status)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, email, display_name, first_name, last_name, git_handle, slack_user_id, telegram_chat_id, delivery_channel, status, created_at, updated_at, teams_user_id, discord_user_id, binding_error;

-- name: createMemberWithAccount :exec
-- Creates a member linked to a human account (active, dashboard-channel). Used by
-- the CreateOrganization compound store; the account must exist before calling this.
INSERT INTO members (id, display_name, delivery_channel, status, account_id, email)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: memberByID :one
SELECT id, email, display_name, first_name, last_name, git_handle, slack_user_id, telegram_chat_id, delivery_channel, status, created_at, updated_at, teams_user_id, discord_user_id, binding_error
FROM members
WHERE id = $1;

-- name: membersByIDs :many
SELECT id, email, display_name, first_name, last_name, git_handle, slack_user_id, telegram_chat_id, delivery_channel, status, created_at, updated_at, teams_user_id, discord_user_id, binding_error
FROM members
WHERE id = ANY($1::uuid[]);

-- name: memberByEmail :one
SELECT id, email, display_name, first_name, last_name, git_handle, slack_user_id, telegram_chat_id, delivery_channel, status, created_at, updated_at, teams_user_id, discord_user_id, binding_error
FROM members
WHERE email = $1;

-- name: memberByAccountID :one
-- Resolves the "primary" member for a human account. account_id is nullable and
-- NON-unique (an account that creates N orgs owns N member rows, and a re-joining
-- human accumulates a dead member alongside a fresh one). A LIVE (authenticatable)
-- member always wins over an offboarded (removed/purged/deactivated/suspended)
-- one so a re-joined account resolves to its fresh membership, not the revoked
-- ghost; among members of equal liveness the most-recent row is chosen for a
-- deterministic identity.
SELECT id, email, display_name, first_name, last_name, git_handle, slack_user_id, telegram_chat_id, delivery_channel, status, created_at, updated_at, teams_user_id, discord_user_id, binding_error
FROM members
WHERE account_id = $1
ORDER BY (status IN ('removed', 'purged', 'deactivated', 'suspended')) ASC, created_at DESC
LIMIT 1;

-- name: memberByAccountInOrg :one
-- Org-scoped variant of memberByAccountID: the account's "primary" member WITHIN
-- one org, resolved via the member's project memberships (project_members →
-- projects.org_id). Backs the redeem idempotency guard, which must be org-scoped
-- so an account that is a live member of org A can still redeem a join code for
-- org B (a global ByAccountID would wrongly report "already member"). A LIVE
-- (authenticatable) member wins over a dead (deactivated/suspended) one, then the
-- most-recent row; removed/purged members no longer own project_members rows
-- (offboard deletes them) so they never surface here at all. Returns ErrNotFound
-- when the account owns no member participating in the org.
SELECT m.id, m.email, m.display_name, m.first_name, m.last_name, m.git_handle, m.slack_user_id, m.telegram_chat_id, m.delivery_channel, m.status, m.created_at, m.updated_at, m.teams_user_id, m.discord_user_id, m.binding_error
FROM members m
JOIN project_members pm ON pm.member_id = m.id
JOIN projects p ON p.id = pm.project_id
WHERE m.account_id = $1 AND p.org_id = $2
ORDER BY (m.status IN ('removed', 'purged', 'deactivated', 'suspended')) ASC, m.created_at DESC
LIMIT 1;

-- name: memberBySlackUserID :one
SELECT id, email, display_name, first_name, last_name, git_handle, slack_user_id, telegram_chat_id, delivery_channel, status, created_at, updated_at, teams_user_id, discord_user_id, binding_error
FROM members
WHERE slack_user_id = $1;

-- name: memberByTelegramChatID :one
SELECT id, email, display_name, first_name, last_name, git_handle, slack_user_id, telegram_chat_id, delivery_channel, status, created_at, updated_at, teams_user_id, discord_user_id, binding_error
FROM members
WHERE telegram_chat_id = $1;

-- name: memberByTeamsUserID :one
-- teams_user_id is NOT NULL DEFAULT ''; callers must never look up an empty
-- string (it would match an arbitrary unbound member) — enforced in the
-- adapter, not here.
SELECT id, email, display_name, first_name, last_name, git_handle, slack_user_id, telegram_chat_id, delivery_channel, status, created_at, updated_at, teams_user_id, discord_user_id, binding_error
FROM members
WHERE teams_user_id = $1;

-- name: memberByDiscordUserID :one
-- discord_user_id is NOT NULL DEFAULT ''; same empty-string caveat as
-- memberByTeamsUserID above.
SELECT id, email, display_name, first_name, last_name, git_handle, slack_user_id, telegram_chat_id, delivery_channel, status, created_at, updated_at, teams_user_id, discord_user_id, binding_error
FROM members
WHERE discord_user_id = $1;

-- name: updateMember :one
UPDATE members
SET display_name     = $2,
    slack_user_id    = $3,
    telegram_chat_id = $4,
    delivery_channel = $5,
    status           = $6,
    email            = $7,
    first_name       = $8,
    last_name        = $9,
    git_handle       = $10,
    -- NULL keeps the existing binding: an account link is only ever added,
    -- never cleared through this update.
    account_id       = COALESCE($11, account_id),
    teams_user_id    = $12,
    discord_user_id  = $13,
    binding_error    = $14,
    updated_at       = NOW()
WHERE id = $1
RETURNING id, email, display_name, first_name, last_name, git_handle, slack_user_id, telegram_chat_id, delivery_channel, status, created_at, updated_at, teams_user_id, discord_user_id, binding_error;

-- name: offboardMember :execrows
-- Remove a member from exactly one organization. Project memberships and org
-- authority are deleted only for $5. The shared members row is terminalized and
-- its chat bindings are freed only when no membership in another organization
-- remains; otherwise the identity stays live for those other organizations.
WITH target AS (
    SELECT
        m.id,
        m.account_id,
        NOT EXISTS (
            SELECT 1
            FROM project_members other_pm
            JOIN projects other_p ON other_p.id = other_pm.project_id
            WHERE other_pm.member_id = m.id
              AND other_p.org_id IS DISTINCT FROM sqlc.arg(org_id)
        ) AS terminal
    FROM members m
    WHERE m.id = sqlc.arg(id)
      AND sqlc.arg(status)::text IN ('removed', 'purged')
      AND EXISTS (
          SELECT 1
          FROM project_members target_pm
          JOIN projects target_p ON target_p.id = target_pm.project_id
          WHERE target_pm.member_id = m.id
            AND target_p.org_id = sqlc.arg(org_id)
      )
),
terminalized AS (
    UPDATE members
    SET status           = sqlc.arg(status),
        email            = sqlc.arg(email),
        display_name     = sqlc.arg(display_name),
        slack_user_id    = '',
        telegram_chat_id = '',
        teams_user_id    = '',
        discord_user_id  = '',
        binding_error    = '',
        delivery_channel = 'dashboard',
        updated_at       = NOW()
    WHERE members.id IN (SELECT target.id FROM target WHERE target.terminal)
    RETURNING members.id
),
purge_project_members AS (
    DELETE FROM project_members pm
    USING projects p, target
    WHERE pm.project_id = p.id
      AND pm.member_id = target.id
      AND p.org_id = sqlc.arg(org_id)
),
purge_org_members AS (
    DELETE FROM org_members om
    USING target
    WHERE om.account_id = target.account_id
      AND om.org_id = sqlc.arg(org_id)
)
SELECT target.id FROM target;

-- name: reactivateRemovedMember :execrows
-- Re-invite a soft-removed member: flip the status back to invited so the
-- invitation lifecycle (event + email) restarts. Guarded on 'removed' so
-- purged members (PII gone) and active members are never touched.
UPDATE members
SET status = 'invited', updated_at = NOW()
WHERE id = $1 AND status = 'removed';

-- name: setMemberAvailability :execrows
-- Flip a member between active and on_leave. return_date is stored only for
-- on_leave (cleared otherwise). Guarded off deactivated/removed/purged so those
-- lifecycle states are only changed through their own paths.
UPDATE members
SET status      = $2,
    return_date = $3,
    updated_at  = NOW()
WHERE id = $1
  AND status IN ('active', 'on_leave', 'invited');

-- name: setMemberActivation :execrows
-- Deactivate (off billing, not routable) or reactivate a member. Reversible.
-- Never touches removed/purged rows.
UPDATE members
SET status      = $2,
    return_date = NULL,
    updated_at  = NOW()
WHERE id = $1
  AND status NOT IN ('removed', 'purged');

-- name: listOrgRoster :many
-- Rich org roster: every member participating in one of the org's projects, with
-- identity, channel + bindings, status, return_date, org-admin flag, external
-- flag, and their per-project expertise tags as a JSON array of
-- {project_id, domains}. The pendingOnly flag ($2) splits the two complementary
-- audiences from one query body:
--   * pendingOnly = false → the roster everyone sees: removed/purged AND the
--     `pending` self-join redeemers (join-code, awaiting approval) are excluded.
--   * pendingOnly = true  → admin-only: ONLY the `pending` members, backing the
--     Team page's "Invited" group.
-- The application handler calls it twice (see list_members.go) and appends the
-- pending set to the roster only for an org admin (RBAC gate in server.go).
SELECT
    m.id, m.email, m.display_name, m.first_name, m.last_name, m.git_handle,
    m.delivery_channel, m.slack_user_id, m.teams_user_id, m.telegram_chat_id,
    m.discord_user_id, m.binding_error, m.status, m.return_date,
    (m.account_id IS NULL)::bool AS external,
    COALESCE(om.role = 'admin', false)::bool AS is_org_admin,
    COALESCE(
        json_agg(json_build_object('project_id', pm.project_id, 'domains', pm.domains))
            FILTER (WHERE pm.project_id IS NOT NULL),
        '[]'
    )::jsonb AS projects
FROM members m
JOIN project_members pm ON pm.member_id = m.id
JOIN projects p ON p.id = pm.project_id AND p.org_id = @org_id
LEFT JOIN org_members om ON om.account_id = m.account_id AND om.org_id = @org_id
WHERE CASE
        WHEN @pending_only::bool THEN m.status = 'pending'
        ELSE m.status NOT IN ('removed', 'purged', 'pending')
      END
GROUP BY m.id, om.role;

-- name: orgMemberByID :one
-- One member's rich record, scoped to the org (returns no row if the member is
-- not part of the caller's org). Same shape as listOrgRoster.
SELECT
    m.id, m.email, m.display_name, m.first_name, m.last_name, m.git_handle,
    m.delivery_channel, m.slack_user_id, m.teams_user_id, m.telegram_chat_id,
    m.discord_user_id, m.binding_error, m.status, m.return_date,
    (m.account_id IS NULL)::bool AS external,
    COALESCE(om.role = 'admin', false)::bool AS is_org_admin,
    COALESCE(
        json_agg(json_build_object('project_id', pm.project_id, 'domains', pm.domains))
            FILTER (WHERE pm.project_id IS NOT NULL),
        '[]'
    )::jsonb AS projects
FROM members m
JOIN project_members pm ON pm.member_id = m.id
JOIN projects p ON p.id = pm.project_id AND p.org_id = $1
LEFT JOIN org_members om ON om.account_id = m.account_id AND om.org_id = $1
WHERE m.id = $2 AND m.status NOT IN ('removed', 'purged')
GROUP BY m.id, om.role;

-- name: memberAccountID :one
SELECT account_id FROM members WHERE id = $1;

-- name: memberOnboardingDismissed :one
-- True once the member has permanently dismissed the Get-started onboarding
-- (the flag is per-member and server-side, so the dismissal survives reloads
-- and follows the member across devices).
SELECT (onboarding_dismissed_at IS NOT NULL)::bool AS dismissed
FROM members
WHERE id = $1;

-- name: setMemberOnboardingDismissed :execrows
-- Mark the Get-started onboarding permanently dismissed for the member.
UPDATE members
SET onboarding_dismissed_at = NOW(), updated_at = NOW()
WHERE id = $1;

-- name: clearMemberOnboardingDismissed :execrows
-- Undo the dismissal (the Get-started page + nav item come back).
UPDATE members
SET onboarding_dismissed_at = NULL, updated_at = NOW()
WHERE id = $1;
