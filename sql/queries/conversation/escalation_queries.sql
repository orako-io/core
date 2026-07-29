-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Escalation sweep: the two rules the minute ticker evaluates. Timeouts are
-- resolved per organization at sweep time: NULL column = product default
-- (the $1 parameter), 0 = rule disabled for that org. Projects without an
-- org (nullable projects.org_id) fall back to the default too.

-- name: orgEscalationSettings :one
SELECT claim_timeout_seconds, alert_timeout_seconds, default_alert_channel_id
FROM organizations
WHERE id = $1;

-- name: updateOrgEscalationSettings :exec
-- default_alert_channel_id: empty string leaves the stored value unchanged,
-- the literal "-" clears it (mirrors UpdateOrganizationSettingsRequest's wire
-- convention for the field). alert_timeout_seconds: a NULL $3 parameter
-- leaves the stored value unchanged (optional int64 presence — an absent
-- field must not silently disable alerts); a non-NULL $3, including 0,
-- overwrites it (0 disables the rule).
UPDATE organizations
SET claim_timeout_seconds    = $2,
    alert_timeout_seconds    = COALESCE($3, alert_timeout_seconds),
    default_alert_channel_id = CASE
        WHEN $4::text = '' THEN default_alert_channel_id
        WHEN $4::text = '-' THEN ''
        ELSE $4::text
    END,
    updated_at               = NOW()
WHERE id = $1;

-- name: unclaimedForNudge :many
-- Pool conversations past their org's nudge timeout (stored column keeps the
-- legacy claim_timeout_seconds name) that were never nudged. "Unanswered" =
-- no specialist label stamped yet (first answer stamps it).
SELECT c.id, c.project_id, c.asker_member_id, c.question, c.context, c.created_at
FROM conversations c
JOIN projects p ON p.id = c.project_id
LEFT JOIN organizations o ON o.id = p.org_id
WHERE c.responder_member_id IS NULL
  AND c.status = 'open'
  AND c.nudged_at IS NULL
  AND EXISTS (
    SELECT 1 FROM conversation_members cc
    WHERE cc.conversation_id = c.id AND cc.invited_at IS NOT NULL
  )
  AND COALESCE(o.claim_timeout_seconds, $1) > 0
  AND c.created_at < NOW() - make_interval(secs => COALESCE(o.claim_timeout_seconds, $1))
ORDER BY c.created_at ASC;

-- name: markConversationNudged :execrows
-- CAS marker so a nudge fires exactly once even across concurrent sweeps.
UPDATE conversations
SET nudged_at  = NOW(),
    updated_at = NOW()
WHERE id = $1 AND nudged_at IS NULL;

-- name: unclaimedForAlert :many
-- Pool conversations past their org's alert timeout that were never posted to
-- the alert channel. Resolves both channel candidates (project's own
-- alert_channel_id, org default_alert_channel_id fallback), the provider kind
-- that owns the project's alert channel, and the active candidate count in
-- the same round trip, so the sweeper needs no further lookups before
-- posting.
--
-- The per-project provider row is picked by preferring one that actually has
-- an alert_channel_id configured (a provider re-saved without one — e.g.
-- rotating a Telegram bot token — must never shadow another provider's
-- configured alert channel), tiebroken deterministically by updated_at DESC.
-- When no configured provider has an alert_channel_id, the most recently
-- updated row is still returned (its empty alert_channel_id) so the org
-- default_alert_channel_id fallback below is unaffected; project_alert_kind
-- is still that row's kind, and the caller posts the org-fallback channel
-- through it too (empty only when the project has no provider configured at
-- all).
--
-- project_alert_kind is the (project, kind) key the caller must resolve the
-- ChannelPoster by — review fit#4: a project with several provider kinds
-- configured must post through the one this query picked, never an
-- ambiguous "the project's provider" lookup that ignores kind entirely.
SELECT c.id, c.project_id, c.question, c.title, c.created_at,
       COALESCE(pp.alert_channel_ids, '{}'::text[]) AS project_alert_channel_ids,
       COALESCE(pp.kind, '') AS project_alert_kind,
       COALESCE(o.default_alert_channel_id, '') AS org_alert_channel_id,
       (
         SELECT COUNT(*) FROM conversation_members cc
         WHERE cc.conversation_id = c.id
           AND cc.invited_at IS NOT NULL AND cc.excluded_at IS NULL
       ) AS candidate_count
FROM conversations c
JOIN projects p ON p.id = c.project_id
LEFT JOIN organizations o ON o.id = p.org_id
LEFT JOIN LATERAL (
    SELECT kind, alert_channel_ids
    FROM project_providers
    WHERE project_id = c.project_id
    ORDER BY (cardinality(alert_channel_ids) > 0) DESC, updated_at DESC
    LIMIT 1
) pp ON true
WHERE c.responder_member_id IS NULL
  AND c.status = 'open'
  AND c.alerted_at IS NULL
  AND EXISTS (
    SELECT 1 FROM conversation_members cc
    WHERE cc.conversation_id = c.id AND cc.invited_at IS NOT NULL
  )
  AND COALESCE(o.alert_timeout_seconds, $1) > 0
  AND c.created_at < NOW() - make_interval(secs => COALESCE(o.alert_timeout_seconds, $1))
ORDER BY c.created_at ASC;

-- name: markConversationAlerted :execrows
-- CAS marker so the alert-channel post fires exactly once even across
-- concurrent sweeps.
UPDATE conversations
SET alerted_at = NOW(),
    updated_at = NOW()
WHERE id = $1 AND alerted_at IS NULL;

-- name: unclaimedForExpiry :many
-- Pool conversations that sat open with no responder past the EXPIRY timeout —
-- the third and final escalation rung. There is no dedicated expiry column: the
-- expiry threshold is the org's effective alert timeout ($1 fills in the
-- product default when the org stored none) multiplied by $2 (the product
-- expiry multiplier). A resolved alert timeout of 0 disables the org's alerts
-- and, with it, expiry — a conversation the org chose never to alert on is
-- never auto-expired either. The once-only guard is the status transition
-- itself (markConversationExpired flips status off 'open'), so no marker column
-- is needed; the WHERE below simply lists still-open, still-unassigned pool
-- conversations old enough to expire.
SELECT c.id, c.project_id, c.asker_member_id, c.question, c.context, c.created_at
FROM conversations c
JOIN projects p ON p.id = c.project_id
LEFT JOIN organizations o ON o.id = p.org_id
WHERE c.responder_member_id IS NULL
  AND c.status = 'open'
  AND EXISTS (
    SELECT 1 FROM conversation_members cc
    WHERE cc.conversation_id = c.id AND cc.invited_at IS NOT NULL
  )
  AND COALESCE(o.alert_timeout_seconds, $1) > 0
  AND c.created_at < NOW() - make_interval(secs => COALESCE(o.alert_timeout_seconds, $1) * $2)
ORDER BY c.created_at ASC;

-- name: markConversationExpired :execrows
-- CAS transition to timed_out: the conversation is abandoned (no responder ever
-- answered) and past the expiry timeout. Guarded on the current state so the
-- transition fires exactly once even across concurrent sweeps and never races a
-- late answer — the moment a responder is stamped or the status leaves 'open',
-- this affects zero rows.
UPDATE conversations
SET status     = 'timed_out',
    updated_at = NOW()
WHERE id = $1 AND status = 'open' AND responder_member_id IS NULL;
