// SPDX-License-Identifier: AGPL-3.0-or-later

package knowledge_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/conversation"
	"github.com/orako-io/core/internal/adapters/knowledge"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// seedResolvedConversation stores a resolved conversation carrying the token in
// its summary, so it is a searchable organic history hit.
func seedResolvedConversation(t *testing.T, store *conversation.Store, projectID, askerID uuid.UUID, question, token string) uuid.UUID {
	t.Helper()

	conv, err := model.NewConversation(uuid.New(), projectID, askerID, question)
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}

	if err := store.CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	if _, err := store.UpdateMetadata(t.Context(), conv.ID, token+" resolved gist", []string{"authservice"}, nil); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}

	if err := store.UpdateStatus(t.Context(), conv.ID, model.ConversationStatusResolved); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	return conv.ID
}

// TestSearchUnionOrganicAndCurated proves the core constraint: with
// IncludeCurated the SearchHistoryHandler returns organic conversations ∪ active
// curated entries as one ranked list of HistoryHit, each carrying the right
// source, and a curated entry is found by the same search that finds a
// conversation. Without IncludeCurated, only conversations appear (the dashboard
// History path is unchanged).
func TestSearchUnionOrganicAndCurated(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	convStore := conversation.NewStore(pool)
	knowStore := knowledge.NewStore(pool)

	orgID := testsupport.SeedOrganization(t, pool)
	proj := testsupport.SeedProjectInOrg(t, pool, orgID)
	asker := testsupport.SeedMember(t, pool)

	token := "zqxunion" + uuid.NewString()[:8]

	convID := seedResolvedConversation(t, convStore, proj, asker, "How does "+token+" work?", token)
	curated := seedEntry(t, knowStore, orgID, proj, asker,
		"Curated "+token+" answer", "Do the thing.", token+" curated gist",
		[]string{"authservice"}, nil)

	handler := query.MustNewSearchHistoryHandler(convStore).WithCurated(knowStore)

	// IncludeCurated: both the conversation and the curated entry come back, each
	// with the right source, under one HistoryHit shape.
	union, err := handler.Handle(t.Context(), query.SearchHistoryQuery{
		OrgID:          orgID,
		ProjectIDs:     []uuid.UUID{proj},
		Query:          token,
		TopK:           50,
		IncludeCurated: true,
	})
	if err != nil {
		t.Fatalf("Handle(IncludeCurated): %v", err)
	}

	bySource := map[uuid.UUID]string{}
	for _, h := range union {
		bySource[h.ConversationID] = h.Source
	}

	if _, ok := bySource[convID]; !ok {
		t.Error("organic conversation missing from the union")
	}

	if bySource[curated.ID] != query.HistorySourceCurated {
		t.Errorf("curated hit missing or mislabeled: source=%q", bySource[curated.ID])
	}

	// The organic hit's source is "conversation" (or empty, treated as such): it
	// must never be labeled curated.
	if src := bySource[convID]; src != "" && src != query.HistorySourceConversation {
		t.Errorf("organic hit source = %q, want conversation", src)
	}

	// Without IncludeCurated the curated entry is excluded (dashboard path).
	organicOnly, err := handler.Handle(t.Context(), query.SearchHistoryQuery{
		OrgID:      orgID,
		ProjectIDs: []uuid.UUID{proj},
		Query:      token,
		TopK:       50,
	})
	if err != nil {
		t.Fatalf("Handle(organic-only): %v", err)
	}

	for _, h := range organicOnly {
		if h.ConversationID == curated.ID {
			t.Error("curated entry leaked into a search without IncludeCurated")
		}
	}
}

// TestDashboardMetricsExcludeCurated proves curated entries never enter the
// responder/response-time analytics: a scope with one conversation and one
// curated entry reports exactly one conversation in the KPI overview.
func TestDashboardMetricsExcludeCurated(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	convStore := conversation.NewStore(pool)
	knowStore := knowledge.NewStore(pool)

	orgID := testsupport.SeedOrganization(t, pool)
	proj := testsupport.SeedProjectInOrg(t, pool, orgID)
	asker := testsupport.SeedMember(t, pool)

	token := "zqxmetric" + uuid.NewString()[:8]

	seedResolvedConversation(t, convStore, proj, asker, "Metric "+token, token)
	// Two curated entries in the same scope must NOT inflate the count.
	seedEntry(t, knowStore, orgID, proj, asker, "curated one", "a", "", nil, nil)
	seedEntry(t, knowStore, orgID, proj, asker, "curated two", "b", "", nil, nil)

	metrics, err := query.MustNewGetDashboardMetricsHandler(convStore).Handle(t.Context(), query.DashboardMetricsQuery{
		OrgID:      orgID,
		ProjectIDs: []uuid.UUID{proj},
		Period:     "all",
	})
	if err != nil {
		t.Fatalf("GetDashboardMetrics: %v", err)
	}

	if metrics.Conversations.Value != 1 {
		t.Errorf("dashboard counts %d conversations; curated entries must be excluded (want 1)", metrics.Conversations.Value)
	}
}
