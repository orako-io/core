// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/service"
)

// TestListProjectsDetailed_CarriesStatsAndRouting proves the Projects-tab read:
// correct per-project counts and per-provider alert routing, and the
// include_archived toggle.
func TestListProjectsDetailed_CarriesStatsAndRouting(t *testing.T) {
	t.Parallel()

	active := uuid.New()
	archived := uuid.New()

	projects := &fakeProjectsDetailedReader{rows: []ProjectDetailRow{
		{ID: active, Name: "Active Project", Archived: false, MemberCount: 3, ConversationCount: 5, ResolvedCount: 7},
		{ID: archived, Name: "Archived Project", Archived: true, MemberCount: 1, ConversationCount: 0, ResolvedCount: 0},
	}}

	routing := newFakeAlertRoutingByProject()
	routing.byProject[active] = []service.ProviderAlertChannel{
		{Kind: "slack", AlertChannelIDs: []string{"C123"}},
	}

	h := MustNewListProjectsDetailedHandler(projects, routing)

	// Default: archived excluded.
	details, err := h.Handle(t.Context(), ListProjectsDetailedQuery{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(details) != 1 {
		t.Fatalf("got %d details, want 1 (archived excluded)", len(details))
	}

	got := details[0]
	if got.ID != active || got.MemberCount != 3 || got.ConversationCount != 5 || got.ResolvedCount != 7 {
		t.Errorf("stats mismatch: got %+v", got)
	}

	if len(got.Routing) != 1 || got.Routing[0].Kind != "slack" || got.Routing[0].AlertChannelIDs[0] != "C123" {
		t.Errorf("routing mismatch: got %+v", got.Routing)
	}

	// include_archived=true surfaces both.
	details, err = h.Handle(t.Context(), ListProjectsDetailedQuery{IncludeArchived: true})
	if err != nil {
		t.Fatalf("Handle(include_archived): %v", err)
	}

	if len(details) != 2 {
		t.Fatalf("got %d details, want 2 (include_archived=true)", len(details))
	}

	var sawArchived bool

	for _, d := range details {
		if d.ID == archived {
			sawArchived = d.Archived
		}
	}

	if !sawArchived {
		t.Error("archived project not returned as archived when include_archived=true")
	}
}

// TestListProjectsDetailed_Empty proves an org with no projects returns an
// empty (not nil) slice.
func TestListProjectsDetailed_Empty(t *testing.T) {
	t.Parallel()

	h := MustNewListProjectsDetailedHandler(&fakeProjectsDetailedReader{}, newFakeAlertRoutingByProject())

	details, err := h.Handle(t.Context(), ListProjectsDetailedQuery{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if details == nil || len(details) != 0 {
		t.Errorf("got %+v, want an empty non-nil slice", details)
	}
}
