// SPDX-License-Identifier: AGPL-3.0-or-later

package conversation_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/conversation"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// seedConvWithMeta creates a conversation in projectID with the given status and
// curated metadata (summary/tags/entities), returning its id.
func seedConvWithMeta(
	t *testing.T,
	store *conversation.Store,
	projectID, askerID uuid.UUID,
	question, summary string,
	status model.ConversationStatus,
	tags, entities []string,
) uuid.UUID {
	t.Helper()

	conv, err := model.NewConversation(uuid.New(), projectID, askerID, question)
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}

	if err := store.CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	if _, err := store.UpdateMetadata(t.Context(), conv.ID, summary, tags, entities); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}

	if status != model.ConversationStatusOpen {
		if err := store.UpdateStatus(t.Context(), conv.ID, status); err != nil {
			t.Fatalf("UpdateStatus(%q): %v", status, err)
		}
	}

	return conv.ID
}

// TestSearchHistoryAllStatusesAndScope proves the P3 engine: it matches the
// generated fts column across ALL statuses (never filtering by status), honors
// the org/project scope exactly, and fuzzy-matches an existing tag via pg_trgm
// even with a mistyped query.
func TestSearchHistoryAllStatusesAndScope(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := conversation.NewStore(pool)

	orgID := testsupport.SeedOrganization(t, pool)
	projA := testsupport.SeedProjectInOrg(t, pool, orgID)
	projB := testsupport.SeedProjectInOrg(t, pool, orgID)
	asker := testsupport.SeedMember(t, pool)

	// A unique lexeme so this test is isolated from any rows a sibling test left.
	token := "zqxhistory" + uuid.NewString()[:8]

	// One conversation per status in project A, all carrying the token in summary.
	statuses := []model.ConversationStatus{
		model.ConversationStatusOpen,
		model.ConversationStatusAnswered,
		model.ConversationStatusResolved,
		model.ConversationStatusTimedOut,
		model.ConversationStatusDismissed,
	}

	want := map[uuid.UUID]bool{}

	for _, st := range statuses {
		id := seedConvWithMeta(t, store, projA, asker,
			"How does "+token+" work?", token+" gist "+string(st),
			st, []string{"authservice"}, []string{"billing"})
		want[id] = true
	}

	// A project-B conversation with the same token: excluded when the scope is
	// restricted to project A, included in the org-wide read.
	bID := seedConvWithMeta(t, store, projB, asker,
		"B side "+token, token+" in B",
		model.ConversationStatusOpen, []string{"authservice"}, nil)

	// Scoped to project A: exactly the five statuses, project B excluded.
	hits, err := store.SearchHistory(t.Context(), orgID, []uuid.UUID{projA}, token, nil, "", 50)
	if err != nil {
		t.Fatalf("SearchHistory(projA): %v", err)
	}

	got := map[uuid.UUID]bool{}
	for _, h := range hits {
		got[h.ConversationID] = true
	}

	for id := range want {
		if !got[id] {
			t.Errorf("project-A hit %v missing — a status was wrongly filtered out", id)
		}
	}

	if got[bID] {
		t.Error("project-B conversation leaked into a project-A-scoped search")
	}

	// Org-wide read (empty projectIDs): project B's conversation now appears.
	orgHits, err := store.SearchHistory(t.Context(), orgID, nil, token, nil, "", 50)
	if err != nil {
		t.Fatalf("SearchHistory(org-wide): %v", err)
	}

	orgGot := map[uuid.UUID]bool{}
	for _, h := range orgHits {
		orgGot[h.ConversationID] = true
	}

	if !orgGot[bID] {
		t.Error("org-wide read must include the project-B conversation")
	}

	// pg_trgm fuzzy arm: a mistyped tag ("authservce") still hits via the
	// search_labels trigram similarity, with no lexical FTS match on the typo.
	fuzzy, err := store.SearchHistory(t.Context(), orgID, []uuid.UUID{projA}, "authservce", nil, "", 50)
	if err != nil {
		t.Fatalf("SearchHistory(fuzzy): %v", err)
	}

	if len(fuzzy) == 0 {
		t.Error("fuzzy query 'authservce' matched nothing — pg_trgm arm not wired")
	}

	// Explicit tag param: a query with no free text but a named tag still hits.
	byTag, err := store.SearchHistory(t.Context(), orgID, []uuid.UUID{projA}, "", []string{"authservice"}, "", 50)
	if err != nil {
		t.Fatalf("SearchHistory(byTag): %v", err)
	}

	if len(byTag) < len(statuses) {
		t.Errorf("tag-overlap arm: got %d hits, want >= %d", len(byTag), len(statuses))
	}
}

func TestSearchHistoryFiltersStatusBeforeLimit(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := conversation.NewStore(pool)
	orgID := testsupport.SeedOrganization(t, pool)
	projectID := testsupport.SeedProjectInOrg(t, pool, orgID)
	askerID := testsupport.SeedMember(t, pool)
	token := "zqxstatus" + uuid.NewString()[:8]

	answeredID := seedConvWithMeta(
		t,
		store,
		projectID,
		askerID,
		token+" answered",
		token,
		model.ConversationStatusAnswered,
		nil,
		nil,
	)
	seedConvWithMeta(
		t,
		store,
		projectID,
		askerID,
		token+" open",
		token,
		model.ConversationStatusOpen,
		nil,
		nil,
	)

	hits, err := store.SearchHistory(
		t.Context(),
		orgID,
		[]uuid.UUID{projectID},
		token,
		nil,
		string(model.ConversationStatusAnswered),
		1,
	)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}

	if len(hits) != 1 || hits[0].ConversationID != answeredID {
		t.Fatalf("hits = %+v, want only answered conversation %s", hits, answeredID)
	}
}
