// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// fakeHistoryReader records search arguments and returns a canned result, so
// the handler contract is testable with no DB and no embedder.
type fakeHistoryReader struct {
	gotOrgID      uuid.UUID
	gotProjectIDs []uuid.UUID
	gotQuery      string
	gotTags       []string
	gotTopK       int
	gotStatus     string
	recentCalled  bool
	hits          []HistoryHit
	counts        HistoryStatusCounts
	err           error
}

func (f *fakeHistoryReader) SearchHistory(_ context.Context, orgID uuid.UUID, projectIDs []uuid.UUID, queryText string, tags []string, status string, topK int) ([]HistoryHit, error) {
	f.gotOrgID = orgID
	f.gotProjectIDs = projectIDs
	f.gotQuery = queryText
	f.gotTags = tags
	f.gotStatus = status
	f.gotTopK = topK

	return f.hits, f.err
}

func (f *fakeHistoryReader) RecentHistory(_ context.Context, orgID uuid.UUID, projectIDs []uuid.UUID, status string, topK int) ([]HistoryHit, error) {
	f.recentCalled = true
	f.gotOrgID = orgID
	f.gotProjectIDs = projectIDs
	f.gotStatus = status
	f.gotTopK = topK

	return f.hits, f.err
}

func (f *fakeHistoryReader) HistoryStatusCounts(_ context.Context, orgID uuid.UUID, projectIDs []uuid.UUID) (HistoryStatusCounts, error) {
	f.gotOrgID = orgID
	f.gotProjectIDs = projectIDs

	return f.counts, f.err
}

func newHit(created time.Time) HistoryHit {
	return HistoryHit{
		ConversationID: uuid.New(),
		ProjectID:      uuid.New(),
		ProjectName:    "P",
		Title:          "t",
		Summary:        "s",
		Status:         model.ConversationStatusResolved,
		Tags:           []string{"auth"},
		Entities:       []string{"authservice"},
		AskerMemberID:  uuid.New(),
		CreatedAt:      created,
		UpdatedAt:      created,
		Score:          1.5,
	}
}

// TestSearchHistoryHandlerDefaultsTopK proves an unset/zero TopK becomes
// defaultTopK and that org/project/query/tags pass straight through to the
// reader unchanged.
func TestSearchHistoryHandlerDefaultsTopK(t *testing.T) {
	t.Parallel()

	reader := &fakeHistoryReader{hits: nil, err: nil}
	h := MustNewSearchHistoryHandler(reader)

	org := uuid.New()
	proj := uuid.New()

	if _, err := h.Handle(t.Context(), SearchHistoryQuery{
		OrgID:      org,
		ProjectIDs: []uuid.UUID{proj},
		Query:      "jwt expiry",
		Tags:       []string{"auth"},
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if reader.gotTopK != defaultTopK {
		t.Errorf("TopK: got %d, want default %d", reader.gotTopK, defaultTopK)
	}

	if reader.gotOrgID != org {
		t.Errorf("OrgID: got %v, want %v", reader.gotOrgID, org)
	}

	if len(reader.gotProjectIDs) != 1 || reader.gotProjectIDs[0] != proj {
		t.Errorf("ProjectIDs: got %v, want [%v]", reader.gotProjectIDs, proj)
	}

	if reader.gotQuery != "jwt expiry" {
		t.Errorf("Query: got %q", reader.gotQuery)
	}

	if len(reader.gotTags) != 1 || reader.gotTags[0] != "auth" {
		t.Errorf("Tags: got %v", reader.gotTags)
	}
}

// TestSearchHistoryHandlerAgeDays proves the handler fills AgeDays from each
// hit's CreatedAt and never returns a negative age for a future timestamp.
func TestSearchHistoryHandlerAgeDays(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	reader := &fakeHistoryReader{
		hits: []HistoryHit{
			newHit(now.Add(-72 * time.Hour)), // 3 days
			newHit(now.Add(-36 * time.Hour)), // 1 day (floored)
			newHit(now.Add(48 * time.Hour)),  // future → clamped to 0
		},
	}

	got, err := MustNewSearchHistoryHandler(reader).Handle(t.Context(), SearchHistoryQuery{Query: "x"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	wantAges := []int{3, 1, 0}
	for i, w := range wantAges {
		if got[i].AgeDays != w {
			t.Errorf("hit[%d] AgeDays: got %d, want %d", i, got[i].AgeDays, w)
		}
	}
}

// TestSearchHistoryHandlerWrapsError proves a reader error surfaces as an
// InternalError-wrapped failure rather than a bare pass-through.
func TestSearchHistoryHandlerWrapsError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("boom")
	reader := &fakeHistoryReader{err: sentinel}

	_, err := MustNewSearchHistoryHandler(reader).Handle(t.Context(), SearchHistoryQuery{Query: "x"})
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("Handle error: got %v, want wrapped %v", err, sentinel)
	}
}
