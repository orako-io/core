// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// Dashboard list/leaderboard caps. Small, fixed windows — the dashboard shows a
// glanceable backlog and leaderboards, not a full list.
const (
	dashboardToHandleCap   = 8
	dashboardResolvedCap   = 5
	dashboardResponderCap  = 5
	dashboardTopicCap      = 5
	dashboardSeriesAllDays = 30 // sparkline length for the unbounded "all" period.
)

// MetricCard is one headline KPI: a value, its signed change vs the previous
// equal-length period, and a per-day series for the sparkline. The unit of Value
// and DeltaPct depends on the card (count & percent for conversations; percent &
// points for response rate; seconds & seconds for median). Series is empty when
// a daily series is disproportionately costly (median) — the frontend then hides
// that card's sparkline.
type MetricCard struct {
	Value    int64
	DeltaPct float64
	Series   []float64
}

// ReuseCard is the "reused from history" deflection KPI. BETA: Available is false
// and Pct/DeltaPts stay 0 until the reuse signal is instrumented.
type ReuseCard struct {
	Available bool
	Pct       float64
	DeltaPts  float64
}

// DashboardConversationRef is one row in the "to handle" list (open + expired,
// most-urgent first). Member ids are resolved to names by the caller.
type DashboardConversationRef struct {
	ConversationID uuid.UUID
	ProjectID      uuid.UUID
	Title          string
	Status         string
	AskerMemberID  uuid.UUID
	CreatedAt      time.Time
}

// DashboardHistoryRef is one row in the "recently added to history" list (latest
// resolved). ResolverMemberID is the assigned responder (uuid.Nil when none).
type DashboardHistoryRef struct {
	ConversationID   uuid.UUID
	Title            string
	Tags             []string
	ResolverMemberID uuid.UUID
	ResolvedAt       time.Time
}

// DashboardResponder is one row in the top-responders leaderboard.
type DashboardResponder struct {
	MemberID    uuid.UUID
	DisplayName string
	AnswerCount int64
}

// DashboardTopic is one row in the top-topics leaderboard (tag frequency).
type DashboardTopic struct {
	Label string
	Count int64
}

// DashboardMetrics is the full org KPI overview for one scope + period.
type DashboardMetrics struct {
	Conversations             MetricCard
	ResponseRate              MetricCard
	MedianFirstResponseSecond MetricCard
	Reuse                     ReuseCard
	OpenCount                 int64
	LeadsCount                int64
	ToHandle                  []DashboardConversationRef
	RecentlyResolved          []DashboardHistoryRef
	TopResponders             []DashboardResponder
	TopTopics                 []DashboardTopic
}

// DashboardDayBucket is one day's conversation totals: Total conversations
// opened that day and how many of them ended answered or resolved.
type DashboardDayBucket struct {
	Day      time.Time
	Total    int
	Answered int
}

// DashboardMetricsReader is the read-side port for the dashboard aggregation
// queries. Satisfied by *conversation.Store; every method scopes by the same
// org/project rule as SearchHistory/ListConversations (archived projects
// excluded, empty projectIDs = org-wide).
type DashboardMetricsReader interface {
	// ConversationWindowStats returns the count of conversations opened in
	// [start, end) and how many of those are answered or resolved.
	ConversationWindowStats(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID, start, end time.Time) (total, answered int, err error)
	// DashboardDailyBuckets returns per-day conversation totals over [start, end),
	// one row per day that has at least one conversation (gaps are zero-filled by
	// the caller).
	DashboardDailyBuckets(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID, start, end time.Time) ([]DashboardDayBucket, error)
	// MedianFirstResponseSeconds returns the median seconds from a conversation's
	// creation (question ts) to its first response message — the first message by
	// someone other than the asker with role answer/follow_up — over conversations
	// opened in [start, end). n is how many conversations had a first response.
	MedianFirstResponseSeconds(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID, start, end time.Time) (median float64, n int, err error)
	// DashboardStateCounts returns the current-state backlog: open conversations
	// and "leads" (timed_out, plus dismissed with no response message).
	DashboardStateCounts(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID) (open, leads int, err error)
	// ToHandleConversations returns the open + expired (timed_out) backlog,
	// most-urgent first (open before expired, oldest first), capped at limit.
	ToHandleConversations(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID, limit int) ([]DashboardConversationRef, error)
	// RecentlyResolvedConversations returns the latest resolved conversations,
	// newest first, capped at limit.
	RecentlyResolvedConversations(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID, limit int) ([]DashboardHistoryRef, error)
	// TopResponders returns the answer count per member over [start, end)
	// (messages with role answer/follow_up/second_opinion), most first, capped at
	// limit, with display names resolved.
	TopResponders(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID, start, end time.Time, limit int) ([]DashboardResponder, error)
	// TopTopics returns conversation tag frequency over conversations opened in
	// [start, end), most first, capped at limit.
	TopTopics(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID, start, end time.Time, limit int) ([]DashboardTopic, error)
}

// DashboardMetricsQuery is the input for the org KPI overview.
type DashboardMetricsQuery struct {
	// OrgID is the caller's organization (org-wide fallback scope when ProjectIDs
	// is empty).
	OrgID uuid.UUID `exhaustruct:"optional"`
	// ProjectIDs scopes the metrics; empty falls back to every non-archived
	// project in OrgID the caller may see (resolved by the transport).
	ProjectIDs []uuid.UUID `exhaustruct:"optional"`
	// Period is one of "7d", "30d", "all". Empty or unknown defaults to "30d".
	Period string `exhaustruct:"optional"`
}

// dashboardWindow is the resolved period: the current window [Start, End), the
// previous equal-length window [PrevStart, Start), whether a previous window
// exists (false for "all"), and how many daily points the sparkline should span.
type dashboardWindow struct {
	Start      time.Time
	End        time.Time
	PrevStart  time.Time
	HasPrev    bool
	SeriesDays int
}

// resolveWindow maps a period string to a concrete dashboardWindow anchored at
// now. "7d"/"30d" are rolling windows with a matching previous window; "all" is
// unbounded (no previous window) with a 30-day sparkline for a recent trend.
func resolveWindow(period string, now time.Time) dashboardWindow {
	switch strings.ToLower(strings.TrimSpace(period)) {
	case "7d":
		return dashboardWindow{
			Start:      now.AddDate(0, 0, -7),
			End:        now,
			PrevStart:  now.AddDate(0, 0, -14),
			HasPrev:    true,
			SeriesDays: 7,
		}
	case "all":
		return dashboardWindow{
			// A fixed epoch lower bound is simpler than a MIN(created_at) probe and
			// covers every real conversation.
			Start:      time.Unix(0, 0).UTC(),
			End:        now,
			PrevStart:  time.Unix(0, 0).UTC(),
			HasPrev:    false,
			SeriesDays: dashboardSeriesAllDays,
		}
	default: // "30d" and any unknown value.
		return dashboardWindow{
			Start:      now.AddDate(0, 0, -30),
			End:        now,
			PrevStart:  now.AddDate(0, 0, -60),
			HasPrev:    true,
			SeriesDays: 30,
		}
	}
}

// GetDashboardMetricsHandler assembles the org KPI overview from the read side.
type GetDashboardMetricsHandler struct {
	reader DashboardMetricsReader
}

// MustNewGetDashboardMetricsHandler builds a handler. It panics on a
// nil dependency.
func MustNewGetDashboardMetricsHandler(reader DashboardMetricsReader) GetDashboardMetricsHandler {
	if reader == nil {
		panic("GetDashboardMetricsHandler requires a non-nil DashboardMetricsReader")
	}

	return GetDashboardMetricsHandler{reader: reader}
}

// Handle runs the aggregation queries and assembles the dashboard metrics. The
// period-sensitive KPIs (conversations, response rate, median first response,
// leaderboards) use the resolved window; the current-state counts and lists
// cover the whole scope.
func (h GetDashboardMetricsHandler) Handle(ctx context.Context, q DashboardMetricsQuery) (DashboardMetrics, error) {
	now := time.Now().UTC()
	w := resolveWindow(q.Period, now)

	conversations, responseRate, err := h.conversationCards(ctx, q, w, now)
	if err != nil {
		return DashboardMetrics{}, err
	}

	median, err := h.medianCard(ctx, q, w)
	if err != nil {
		return DashboardMetrics{}, err
	}

	open, leads, err := h.reader.DashboardStateCounts(ctx, q.OrgID, q.ProjectIDs)
	if err != nil {
		return DashboardMetrics{}, errs.InternalError{Err: fmt.Errorf("dashboard state counts: %w", err)}
	}

	toHandle, err := h.reader.ToHandleConversations(ctx, q.OrgID, q.ProjectIDs, dashboardToHandleCap)
	if err != nil {
		return DashboardMetrics{}, errs.InternalError{Err: fmt.Errorf("dashboard to-handle: %w", err)}
	}

	resolved, err := h.reader.RecentlyResolvedConversations(ctx, q.OrgID, q.ProjectIDs, dashboardResolvedCap)
	if err != nil {
		return DashboardMetrics{}, errs.InternalError{Err: fmt.Errorf("dashboard recently-resolved: %w", err)}
	}

	responders, err := h.reader.TopResponders(ctx, q.OrgID, q.ProjectIDs, w.Start, w.End, dashboardResponderCap)
	if err != nil {
		return DashboardMetrics{}, errs.InternalError{Err: fmt.Errorf("dashboard top responders: %w", err)}
	}

	topics, err := h.reader.TopTopics(ctx, q.OrgID, q.ProjectIDs, w.Start, w.End, dashboardTopicCap)
	if err != nil {
		return DashboardMetrics{}, errs.InternalError{Err: fmt.Errorf("dashboard top topics: %w", err)}
	}

	return DashboardMetrics{
		Conversations:             conversations,
		ResponseRate:              responseRate,
		MedianFirstResponseSecond: median,
		// BETA: no reuse instrumentation yet — a placeholder card, always
		// unavailable so the frontend renders the BETA badge.
		Reuse:            ReuseCard{Available: false, Pct: 0, DeltaPts: 0},
		OpenCount:        int64(open),
		LeadsCount:       int64(leads),
		ToHandle:         toHandle,
		RecentlyResolved: resolved,
		TopResponders:    responders,
		TopTopics:        topics,
	}, nil
}

// conversationCards builds the conversations and response-rate KPI cards. Both
// share the windowed totals (current vs previous) for their delta and the daily
// buckets for their sparkline.
func (h GetDashboardMetricsHandler) conversationCards(
	ctx context.Context,
	q DashboardMetricsQuery,
	w dashboardWindow,
	now time.Time,
) (conversations, responseRate MetricCard, err error) {
	curTotal, curAnswered, err := h.reader.ConversationWindowStats(ctx, q.OrgID, q.ProjectIDs, w.Start, w.End)
	if err != nil {
		return MetricCard{}, MetricCard{}, errs.InternalError{Err: fmt.Errorf("dashboard window stats: %w", err)}
	}

	var (
		prevTotal    int
		prevAnswered int
	)

	if w.HasPrev {
		prevTotal, prevAnswered, err = h.reader.ConversationWindowStats(ctx, q.OrgID, q.ProjectIDs, w.PrevStart, w.Start)
		if err != nil {
			return MetricCard{}, MetricCard{}, errs.InternalError{Err: fmt.Errorf("dashboard prev window stats: %w", err)}
		}
	}

	// Sparklines span the last SeriesDays whole days ending today.
	seriesStart := dayStart(now.AddDate(0, 0, -(w.SeriesDays - 1)))

	buckets, err := h.reader.DashboardDailyBuckets(ctx, q.OrgID, q.ProjectIDs, seriesStart, now)
	if err != nil {
		return MetricCard{}, MetricCard{}, errs.InternalError{Err: fmt.Errorf("dashboard daily buckets: %w", err)}
	}

	totalSeries, rateSeries := dailySeries(buckets, seriesStart, w.SeriesDays)

	conversations = MetricCard{
		Value:    int64(curTotal),
		DeltaPct: percentChange(float64(curTotal), float64(prevTotal), w.HasPrev),
		Series:   totalSeries,
	}

	curRate := rateOf(curAnswered, curTotal)
	responseRate = MetricCard{
		Value:    int64(curRate + 0.5), //nolint:mnd // round to nearest whole percent.
		DeltaPct: 0,
		Series:   rateSeries,
	}

	if w.HasPrev {
		// Response-rate delta is in POINTS (current rate − previous rate), not a
		// percent change of the rate.
		responseRate.DeltaPct = curRate - rateOf(prevAnswered, prevTotal)
	}

	return conversations, responseRate, nil
}

// medianCard builds the median-first-response card. Its daily series is omitted
// (empty) — a per-day median join is disproportionately costly and the frontend
// hides the sparkline when the series is empty.
func (h GetDashboardMetricsHandler) medianCard(ctx context.Context, q DashboardMetricsQuery, w dashboardWindow) (MetricCard, error) {
	curMedian, _, err := h.reader.MedianFirstResponseSeconds(ctx, q.OrgID, q.ProjectIDs, w.Start, w.End)
	if err != nil {
		return MetricCard{}, errs.InternalError{Err: fmt.Errorf("dashboard median first response: %w", err)}
	}

	card := MetricCard{Value: int64(curMedian + 0.5), DeltaPct: 0, Series: nil} //nolint:mnd // round to nearest second.

	if w.HasPrev {
		prevMedian, _, err := h.reader.MedianFirstResponseSeconds(ctx, q.OrgID, q.ProjectIDs, w.PrevStart, w.Start)
		if err != nil {
			return MetricCard{}, errs.InternalError{Err: fmt.Errorf("dashboard prev median first response: %w", err)}
		}

		// Delta in seconds; negative = faster than the previous period.
		card.DeltaPct = curMedian - prevMedian
	}

	return card, nil
}

// dayStart truncates t to midnight UTC.
func dayStart(t time.Time) time.Time {
	y, m, d := t.UTC().Date()

	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// dailySeries zero-fills the sparkline over days whole days starting at start:
// totalSeries is the per-day conversation count, rateSeries the per-day
// answered-or-resolved rate (0-100, 0 on a day with no conversations).
func dailySeries(buckets []DashboardDayBucket, start time.Time, days int) (totalSeries, rateSeries []float64) {
	byDay := make(map[string]DashboardDayBucket, len(buckets))
	for _, b := range buckets {
		byDay[dayStart(b.Day).Format("2006-01-02")] = b
	}

	totalSeries = make([]float64, days)
	rateSeries = make([]float64, days)

	for i := range days {
		key := start.AddDate(0, 0, i).Format("2006-01-02")
		b := byDay[key]
		totalSeries[i] = float64(b.Total)
		rateSeries[i] = rateOf(b.Answered, b.Total)
	}

	return totalSeries, rateSeries
}

// rateOf returns answered/total as a percent 0-100 (0 when total is 0).
func rateOf(answered, total int) float64 {
	if total <= 0 {
		return 0
	}

	return float64(answered) * 100.0 / float64(total)
}

// percentChange returns the signed percent change from prev to cur. With no
// previous window (hasPrev false) or a zero baseline it returns 0 when cur is
// also 0, else 100 (all-new).
func percentChange(cur, prev float64, hasPrev bool) float64 {
	if !hasPrev || prev == 0 {
		if cur == 0 {
			return 0
		}

		return 100
	}

	return (cur - prev) / prev * 100.0
}
