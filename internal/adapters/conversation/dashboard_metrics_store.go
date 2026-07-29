// SPDX-License-Identifier: AGPL-3.0-or-later

package conversation

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/pkg/pgconv"
)

var _ query.DashboardMetricsReader = (*Store)(nil)

// dashboardScopeCTE is the shared scope selector for every dashboard read: the
// non-archived projects in org_id ($1), optionally narrowed to projectIDs ($2)
// — an empty array falls back to the org-wide read. Identical to
// searchHistorySQL's scope so the dashboard, History tab, and conversation list
// all see the same set.
const dashboardScopeCTE = `
WITH scope AS (
	SELECT p.id
	FROM projects p
	WHERE p.archived_at IS NULL
	  AND (p.org_id = $1 OR $1 = '` + pgconv.NilOrgID + `'::uuid)
	  AND (cardinality($2::uuid[]) = 0 OR p.id = ANY($2::uuid[]))
)`

// conversationWindowStatsSQL counts conversations opened in [$3, $4) and how
// many ended answered or resolved.
const conversationWindowStatsSQL = dashboardScopeCTE + `
SELECT
	count(*),
	count(*) FILTER (WHERE c.status IN ('answered', 'resolved'))
FROM conversations c
WHERE c.project_id IN (SELECT id FROM scope)
  AND c.created_at >= $3
  AND c.created_at < $4
`

// dashboardDailyBucketsSQL buckets conversations opened in [$3, $4) by UTC day,
// returning the per-day total and answered-or-resolved count. Only days with at
// least one conversation appear; the caller zero-fills gaps.
const dashboardDailyBucketsSQL = dashboardScopeCTE + `
SELECT
	date_trunc('day', c.created_at AT TIME ZONE 'UTC') AS day,
	count(*),
	count(*) FILTER (WHERE c.status IN ('answered', 'resolved'))
FROM conversations c
WHERE c.project_id IN (SELECT id FROM scope)
  AND c.created_at >= $3
  AND c.created_at < $4
GROUP BY day
ORDER BY day
`

// medianFirstResponseSQL computes the median seconds from a conversation's
// creation (question ts) to its first response — the earliest message by someone
// other than the asker with role answer/follow_up — over conversations opened in
// [$3, $4). author_member_id IS DISTINCT FROM asker treats a NULL (agent) author
// as "not the asker". percentile_cont interpolates the median; NULL (no rows)
// surfaces as median 0, n 0.
const medianFirstResponseSQL = dashboardScopeCTE + `,
firsts AS (
	SELECT c.id, extract(epoch FROM (min(m.created_at) - c.created_at)) AS secs
	FROM conversations c
	JOIN messages m ON m.conversation_id = c.id
	WHERE c.project_id IN (SELECT id FROM scope)
	  AND c.created_at >= $3
	  AND c.created_at < $4
	  AND m.role IN ('answer', 'follow_up')
	  AND m.author_member_id IS DISTINCT FROM c.asker_member_id
	GROUP BY c.id, c.created_at
)
SELECT
	coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY secs), 0),
	count(*)
FROM firsts
`

// dashboardStateCountsSQL returns the current-state backlog over the whole
// scope: open conversations, and "leads" = timed_out plus dismissed with no
// response message (a message by someone other than the asker with role
// answer/follow_up).
const dashboardStateCountsSQL = dashboardScopeCTE + `
SELECT
	count(*) FILTER (WHERE c.status = 'open'),
	count(*) FILTER (
		WHERE c.status = 'timed_out'
		   OR (c.status = 'dismissed' AND NOT EXISTS (
		         SELECT 1 FROM messages m
		         WHERE m.conversation_id = c.id
		           AND m.role IN ('answer', 'follow_up')
		           AND m.author_member_id IS DISTINCT FROM c.asker_member_id
		       ))
	)
FROM conversations c
WHERE c.project_id IN (SELECT id FROM scope)
`

// toHandleConversationsSQL lists the open + expired (timed_out) backlog,
// most-urgent first: open before expired, then oldest first (longest waiting),
// capped at $3.
const toHandleConversationsSQL = dashboardScopeCTE + `
SELECT c.id, c.project_id, c.title, c.question, c.status, c.asker_member_id, c.created_at
FROM conversations c
WHERE c.project_id IN (SELECT id FROM scope)
  AND c.status IN ('open', 'timed_out')
ORDER BY (c.status = 'open') DESC, c.created_at ASC
LIMIT $3
`

// recentlyResolvedConversationsSQL lists the latest resolved conversations,
// newest first (resolved bumps updated_at), capped at $3.
const recentlyResolvedConversationsSQL = dashboardScopeCTE + `
SELECT c.id, c.title, c.question, c.tags, c.responder_member_id, c.updated_at
FROM conversations c
WHERE c.project_id IN (SELECT id FROM scope)
  AND c.status = 'resolved'
ORDER BY c.updated_at DESC
LIMIT $3
`

// topRespondersSQL counts response messages (role answer/follow_up/second_opinion
// by a non-NULL author) per member over messages authored in [$3, $4), joining
// members for the display name. Ordered by count desc, capped at $5.
const topRespondersSQL = dashboardScopeCTE + `
SELECT m.author_member_id, coalesce(mem.display_name, ''), count(*)
FROM messages m
JOIN conversations c ON c.id = m.conversation_id
LEFT JOIN members mem ON mem.id = m.author_member_id
WHERE c.project_id IN (SELECT id FROM scope)
  AND m.author_member_id IS NOT NULL
  AND m.role IN ('answer', 'follow_up', 'second_opinion')
  AND m.created_at >= $3
  AND m.created_at < $4
GROUP BY m.author_member_id, mem.display_name
ORDER BY count(*) DESC, m.author_member_id
LIMIT $5
`

// topTopicsSQL counts conversation tag frequency over conversations opened in
// [$3, $4). LATERAL unnest expands each row's tags[]. Ordered by count desc,
// capped at $5.
const topTopicsSQL = dashboardScopeCTE + `
SELECT tag, count(*)
FROM conversations c
CROSS JOIN LATERAL unnest(c.tags) AS tag
WHERE c.project_id IN (SELECT id FROM scope)
  AND c.created_at >= $3
  AND c.created_at < $4
GROUP BY tag
ORDER BY count(*) DESC, tag ASC
LIMIT $5
`

// ConversationWindowStats returns the count of conversations opened in
// [start, end) and how many are answered or resolved.
func (s *Store) ConversationWindowStats(
	ctx context.Context,
	orgID uuid.UUID,
	projectIDs []uuid.UUID,
	start, end time.Time,
) (int, int, error) {
	var total, answered int

	row := s.pool.QueryRow(ctx, conversationWindowStatsSQL, orgID, uuidsOrEmpty(projectIDs), start, end)
	if err := row.Scan(&total, &answered); err != nil {
		return 0, 0, fmt.Errorf("dashboard window stats: %w", adaptererr.Decode(err))
	}

	return total, answered, nil
}

// DashboardDailyBuckets returns per-day conversation totals over [start, end).
func (s *Store) DashboardDailyBuckets(
	ctx context.Context,
	orgID uuid.UUID,
	projectIDs []uuid.UUID,
	start, end time.Time,
) ([]query.DashboardDayBucket, error) {
	rows, err := s.pool.Query(ctx, dashboardDailyBucketsSQL, orgID, uuidsOrEmpty(projectIDs), start, end)
	if err != nil {
		return nil, fmt.Errorf("dashboard daily buckets: %w", adaptererr.Decode(err))
	}
	defer rows.Close()

	var out []query.DashboardDayBucket

	for rows.Next() {
		var b query.DashboardDayBucket

		if err := rows.Scan(&b.Day, &b.Total, &b.Answered); err != nil {
			return nil, fmt.Errorf("scanning daily bucket row: %w", err)
		}

		out = append(out, b)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating daily buckets: %w", adaptererr.Decode(err))
	}

	return out, nil
}

// MedianFirstResponseSeconds returns the median question→first-response latency
// in seconds and the number of conversations that had a first response.
func (s *Store) MedianFirstResponseSeconds(
	ctx context.Context,
	orgID uuid.UUID,
	projectIDs []uuid.UUID,
	start, end time.Time,
) (float64, int, error) {
	var (
		median float64
		n      int
	)

	row := s.pool.QueryRow(ctx, medianFirstResponseSQL, orgID, uuidsOrEmpty(projectIDs), start, end)
	if err := row.Scan(&median, &n); err != nil {
		return 0, 0, fmt.Errorf("dashboard median first response: %w", adaptererr.Decode(err))
	}

	return median, n, nil
}

// DashboardStateCounts returns the open and leads current-state counts.
func (s *Store) DashboardStateCounts(
	ctx context.Context,
	orgID uuid.UUID,
	projectIDs []uuid.UUID,
) (int, int, error) {
	var open, leads int

	row := s.pool.QueryRow(ctx, dashboardStateCountsSQL, orgID, uuidsOrEmpty(projectIDs))
	if err := row.Scan(&open, &leads); err != nil {
		return 0, 0, fmt.Errorf("dashboard state counts: %w", adaptererr.Decode(err))
	}

	return open, leads, nil
}

// ToHandleConversations returns the open + expired backlog, most-urgent first.
func (s *Store) ToHandleConversations(
	ctx context.Context,
	orgID uuid.UUID,
	projectIDs []uuid.UUID,
	limit int,
) ([]query.DashboardConversationRef, error) {
	rows, err := s.pool.Query(ctx, toHandleConversationsSQL, orgID, uuidsOrEmpty(projectIDs), limit)
	if err != nil {
		return nil, fmt.Errorf("dashboard to-handle: %w", adaptererr.Decode(err))
	}
	defer rows.Close()

	var out []query.DashboardConversationRef

	for rows.Next() {
		var (
			ref      query.DashboardConversationRef
			title    string
			question string
			status   string
		)

		if err := rows.Scan(&ref.ConversationID, &ref.ProjectID, &title, &question, &status, &ref.AskerMemberID, &ref.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning to-handle row: %w", err)
		}

		ref.Title = model.DisplayTitle(title, question)
		ref.Status = status
		out = append(out, ref)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating to-handle: %w", adaptererr.Decode(err))
	}

	return out, nil
}

// RecentlyResolvedConversations returns the latest resolved conversations.
func (s *Store) RecentlyResolvedConversations(
	ctx context.Context,
	orgID uuid.UUID,
	projectIDs []uuid.UUID,
	limit int,
) ([]query.DashboardHistoryRef, error) {
	rows, err := s.pool.Query(ctx, recentlyResolvedConversationsSQL, orgID, uuidsOrEmpty(projectIDs), limit)
	if err != nil {
		return nil, fmt.Errorf("dashboard recently-resolved: %w", adaptererr.Decode(err))
	}
	defer rows.Close()

	var out []query.DashboardHistoryRef

	for rows.Next() {
		var (
			ref      query.DashboardHistoryRef
			title    string
			question string
			resolver pgtype.UUID
		)

		if err := rows.Scan(&ref.ConversationID, &title, &question, &ref.Tags, &resolver, &ref.ResolvedAt); err != nil {
			return nil, fmt.Errorf("scanning recently-resolved row: %w", err)
		}

		ref.Title = model.DisplayTitle(title, question)
		ref.ResolverMemberID = pgconv.UUIDFromPgtype(resolver)
		out = append(out, ref)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating recently-resolved: %w", adaptererr.Decode(err))
	}

	return out, nil
}

// TopResponders returns the answer count per member over [start, end).
func (s *Store) TopResponders(
	ctx context.Context,
	orgID uuid.UUID,
	projectIDs []uuid.UUID,
	start, end time.Time,
	limit int,
) ([]query.DashboardResponder, error) {
	rows, err := s.pool.Query(ctx, topRespondersSQL, orgID, uuidsOrEmpty(projectIDs), start, end, limit)
	if err != nil {
		return nil, fmt.Errorf("dashboard top responders: %w", adaptererr.Decode(err))
	}
	defer rows.Close()

	var out []query.DashboardResponder

	for rows.Next() {
		var r query.DashboardResponder

		if err := rows.Scan(&r.MemberID, &r.DisplayName, &r.AnswerCount); err != nil {
			return nil, fmt.Errorf("scanning top responder row: %w", err)
		}

		out = append(out, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating top responders: %w", adaptererr.Decode(err))
	}

	return out, nil
}

// TopTopics returns conversation tag frequency over [start, end).
func (s *Store) TopTopics(
	ctx context.Context,
	orgID uuid.UUID,
	projectIDs []uuid.UUID,
	start, end time.Time,
	limit int,
) ([]query.DashboardTopic, error) {
	rows, err := s.pool.Query(ctx, topTopicsSQL, orgID, uuidsOrEmpty(projectIDs), start, end, limit)
	if err != nil {
		return nil, fmt.Errorf("dashboard top topics: %w", adaptererr.Decode(err))
	}
	defer rows.Close()

	var out []query.DashboardTopic

	for rows.Next() {
		var t query.DashboardTopic

		if err := rows.Scan(&t.Label, &t.Count); err != nil {
			return nil, fmt.Errorf("scanning top topic row: %w", err)
		}

		out = append(out, t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating top topics: %w", adaptererr.Decode(err))
	}

	return out, nil
}

// uuidsOrEmpty coerces a nil projectIDs slice to a non-nil empty slice so the
// scope CTE's cardinality($2)=0 org-wide fallback fires (pgx would otherwise
// encode nil as NULL, not an empty array).
func uuidsOrEmpty(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}

	return ids
}
