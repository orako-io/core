// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeDashboardReader returns canned aggregates and records the windows it was
// asked about, so the handler's period mapping and delta math are testable with
// no DB.
type fakeDashboardReader struct {
	// windowStats keyed by whether the window is the current one (end == now-ish);
	// we distinguish by start being the more-recent of the two.
	curTotal, curAnswered   int
	prevTotal, prevAnswered int
	curMedian, prevMedian   float64
	statStarts              []time.Time
	buckets                 []DashboardDayBucket
}

func (f *fakeDashboardReader) ConversationWindowStats(_ context.Context, _ uuid.UUID, _ []uuid.UUID, start, end time.Time) (int, int, error) {
	f.statStarts = append(f.statStarts, start)
	// The previous window ends where the current window starts; the current
	// window ends at now. Use end proximity to "now" to decide which to return.
	if time.Since(end) < time.Hour {
		return f.curTotal, f.curAnswered, nil
	}

	return f.prevTotal, f.prevAnswered, nil
}

func (f *fakeDashboardReader) DashboardDailyBuckets(_ context.Context, _ uuid.UUID, _ []uuid.UUID, _, _ time.Time) ([]DashboardDayBucket, error) {
	return f.buckets, nil
}

func (f *fakeDashboardReader) MedianFirstResponseSeconds(_ context.Context, _ uuid.UUID, _ []uuid.UUID, _, end time.Time) (float64, int, error) {
	if time.Since(end) < time.Hour {
		return f.curMedian, 1, nil
	}

	return f.prevMedian, 1, nil
}

func (f *fakeDashboardReader) DashboardStateCounts(_ context.Context, _ uuid.UUID, _ []uuid.UUID) (int, int, error) {
	return 3, 2, nil
}

func (f *fakeDashboardReader) ToHandleConversations(_ context.Context, _ uuid.UUID, _ []uuid.UUID, _ int) ([]DashboardConversationRef, error) {
	return []DashboardConversationRef{{ConversationID: uuid.New(), ProjectID: uuid.New(), Title: "t", Status: "open", AskerMemberID: uuid.New(), CreatedAt: time.Now()}}, nil
}

func (f *fakeDashboardReader) RecentlyResolvedConversations(_ context.Context, _ uuid.UUID, _ []uuid.UUID, _ int) ([]DashboardHistoryRef, error) {
	return nil, nil
}

func (f *fakeDashboardReader) TopResponders(_ context.Context, _ uuid.UUID, _ []uuid.UUID, _, _ time.Time, _ int) ([]DashboardResponder, error) {
	return []DashboardResponder{{MemberID: uuid.New(), DisplayName: "Marc", AnswerCount: 4}}, nil
}

func (f *fakeDashboardReader) TopTopics(_ context.Context, _ uuid.UUID, _ []uuid.UUID, _, _ time.Time, _ int) ([]DashboardTopic, error) {
	return []DashboardTopic{{Label: "auth", Count: 5}}, nil
}

// TestDashboardMetricsThirtyDayDefaults proves an empty period maps to a 30-day
// window (30 sparkline points), the conversations delta is a percent change vs
// the previous window, the response rate is a whole percent, the median delta is
// in seconds (negative = faster), and the reuse card is an unavailable BETA
// placeholder.
func TestDashboardMetricsThirtyDayDefaults(t *testing.T) {
	t.Parallel()

	reader := &fakeDashboardReader{
		curTotal: 20, curAnswered: 15,
		prevTotal: 10, prevAnswered: 4,
		curMedian: 100, prevMedian: 160,
	}

	got, err := MustNewGetDashboardMetricsHandler(reader).Handle(t.Context(), DashboardMetricsQuery{Period: ""})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if got.Conversations.Value != 20 {
		t.Errorf("conversations value: got %d, want 20", got.Conversations.Value)
	}

	// (20-10)/10*100 = 100.
	if got.Conversations.DeltaPct != 100 {
		t.Errorf("conversations delta: got %v, want 100", got.Conversations.DeltaPct)
	}

	if len(got.Conversations.Series) != 30 {
		t.Errorf("series length: got %d, want 30", len(got.Conversations.Series))
	}

	// 15/20 = 75%.
	if got.ResponseRate.Value != 75 {
		t.Errorf("response rate: got %d, want 75", got.ResponseRate.Value)
	}

	// 75% - 40% = 35 points.
	if got.ResponseRate.DeltaPct != 35 {
		t.Errorf("response rate delta: got %v, want 35 points", got.ResponseRate.DeltaPct)
	}

	if got.MedianFirstResponseSecond.Value != 100 {
		t.Errorf("median: got %d, want 100", got.MedianFirstResponseSecond.Value)
	}

	// 100 - 160 = -60 (faster).
	if got.MedianFirstResponseSecond.DeltaPct != -60 {
		t.Errorf("median delta: got %v, want -60", got.MedianFirstResponseSecond.DeltaPct)
	}

	// Median daily series is intentionally omitted.
	if got.MedianFirstResponseSecond.Series != nil {
		t.Errorf("median series: got %v, want nil (omitted)", got.MedianFirstResponseSecond.Series)
	}

	if got.Reuse.Available {
		t.Error("reuse card must be unavailable (BETA placeholder)")
	}

	if got.OpenCount != 3 || got.LeadsCount != 2 {
		t.Errorf("state counts: got open=%d leads=%d, want 3/2", got.OpenCount, got.LeadsCount)
	}
}

// TestDashboardMetricsAllPeriodNoPrev proves the "all" period skips the previous
// window (delta 0) and still returns a bounded 30-point sparkline.
func TestDashboardMetricsAllPeriodNoPrev(t *testing.T) {
	t.Parallel()

	reader := &fakeDashboardReader{curTotal: 42, curAnswered: 21}

	got, err := MustNewGetDashboardMetricsHandler(reader).Handle(t.Context(), DashboardMetricsQuery{Period: "all"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if got.Conversations.DeltaPct != 100 {
		// No previous window + non-zero current = all-new (100).
		t.Errorf("all-period delta: got %v, want 100", got.Conversations.DeltaPct)
	}

	if got.ResponseRate.DeltaPct != 0 {
		t.Errorf("all-period rate delta: got %v, want 0 (no prev)", got.ResponseRate.DeltaPct)
	}

	if len(got.Conversations.Series) != dashboardSeriesAllDays {
		t.Errorf("all-period series length: got %d, want %d", len(got.Conversations.Series), dashboardSeriesAllDays)
	}
}

// TestDashboardMetricsSevenDayWindow proves "7d" maps to a 7-point sparkline.
func TestDashboardMetricsSevenDayWindow(t *testing.T) {
	t.Parallel()

	got, err := MustNewGetDashboardMetricsHandler(&fakeDashboardReader{}).Handle(t.Context(), DashboardMetricsQuery{Period: "7d"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(got.Conversations.Series) != 7 {
		t.Errorf("7d series length: got %d, want 7", len(got.Conversations.Series))
	}
}
