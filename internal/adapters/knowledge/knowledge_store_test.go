// SPDX-License-Identifier: AGPL-3.0-or-later

package knowledge_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/knowledge"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// seedEntry creates a curated entry in orgID (project-scoped when projectID is
// non-nil, org-wide otherwise) and returns it.
func seedEntry(
	t *testing.T,
	store *knowledge.Store,
	orgID, projectID, authorID uuid.UUID,
	question, answer, summary string,
	tags, entities []string,
) model.KnowledgeEntry {
	t.Helper()

	entry, err := model.NewKnowledgeEntry(uuid.New(), orgID, projectID, authorID, question, answer, summary, tags, entities)
	if err != nil {
		t.Fatalf("NewKnowledgeEntry: %v", err)
	}

	stored, err := store.CreateEntry(t.Context(), entry)
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}

	return stored
}

// TestKnowledgeCRUD proves the full lifecycle: create, read-by-id, update
// (content + status), and mark-stale, each round-tripping through Postgres.
func TestKnowledgeCRUD(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := knowledge.NewStore(pool)

	orgID := testsupport.SeedOrganization(t, pool)
	proj := testsupport.SeedProjectInOrg(t, pool, orgID)
	author := testsupport.SeedMember(t, pool)

	created := seedEntry(t, store, orgID, proj, author,
		"How do I configure Discord?", "Set ORAKO_DISCORD_TOKEN and restart.", "discord setup",
		[]string{"Discord", "discord"}, []string{"config"})

	// Normalizers ran: dedup + lowercase on tags, summary trimmed.
	if len(created.Tags) != 1 || created.Tags[0] != "discord" {
		t.Errorf("tags not normalized: %v", created.Tags)
	}

	if created.Source != model.KnowledgeSourceCurated || created.Status != model.KnowledgeStatusActive {
		t.Errorf("defaults wrong: source=%q status=%q", created.Source, created.Status)
	}

	got, err := store.EntryByID(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("EntryByID: %v", err)
	}

	if got.Answer != "Set ORAKO_DISCORD_TOKEN and restart." || got.AuthorMemberID != author {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// Update content + status.
	got.Question = "How do I set up Discord?"
	got.Answer = "Configure the bot token."
	got.Status = model.KnowledgeStatusStale

	if _, err := store.UpdateEntry(t.Context(), got); err != nil {
		t.Fatalf("UpdateEntry: %v", err)
	}

	after, err := store.EntryByID(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("EntryByID after update: %v", err)
	}

	if after.Question != "How do I set up Discord?" || after.Status != model.KnowledgeStatusStale {
		t.Errorf("update not persisted: %+v", after)
	}

	// MarkStale on an already-stale entry is idempotent; flip back to active first.
	after.Status = model.KnowledgeStatusActive
	if _, err := store.UpdateEntry(t.Context(), after); err != nil {
		t.Fatalf("re-activate: %v", err)
	}

	if err := store.MarkStale(t.Context(), created.ID); err != nil {
		t.Fatalf("MarkStale: %v", err)
	}

	stale, err := store.EntryByID(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("EntryByID after mark-stale: %v", err)
	}

	if stale.Status != model.KnowledgeStatusStale {
		t.Errorf("MarkStale did not set status=stale: %q", stale.Status)
	}
}

// TestSearchKnowledgeScopeAndStatus proves the curated search: FTS match, the
// org-wide (project_id NULL) vs project scope, the pg_trgm fuzzy arm, and that
// only ACTIVE entries surface — a stale one drops out.
func TestSearchKnowledgeScopeAndStatus(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := knowledge.NewStore(pool)

	orgID := testsupport.SeedOrganization(t, pool)
	projA := testsupport.SeedProjectInOrg(t, pool, orgID)
	projB := testsupport.SeedProjectInOrg(t, pool, orgID)
	author := testsupport.SeedMember(t, pool)

	token := "zqxknow" + uuid.NewString()[:8]

	// Project-A entry, org-wide entry, project-B entry — all carrying the token.
	aEntry := seedEntry(t, store, orgID, projA, author,
		"How does "+token+" work?", "A answer", token+" gist",
		[]string{"authservice"}, []string{"billing"})
	orgWide := seedEntry(t, store, orgID, uuid.Nil, author,
		"Org-wide "+token, "org answer", token+" org",
		[]string{"authservice"}, nil)
	bEntry := seedEntry(t, store, orgID, projB, author,
		"B side "+token, "B answer", token+" in B",
		nil, nil)

	// Scoped to project A: the A entry and the org-wide entry hit; B is excluded.
	hits, err := store.SearchKnowledge(t.Context(), orgID, []uuid.UUID{projA}, token, nil, 50)
	if err != nil {
		t.Fatalf("SearchKnowledge(projA): %v", err)
	}

	got := hitSet(hits)
	if !got[aEntry.ID] || !got[orgWide.ID] {
		t.Errorf("project-A search must include the A entry and the org-wide entry; got %v", got)
	}

	if got[bEntry.ID] {
		t.Error("project-B entry leaked into a project-A-scoped curated search")
	}

	// Every curated hit is shaped as a reusable resolved answer.
	for _, h := range hits {
		if h.Source != query.HistorySourceCurated {
			t.Errorf("curated hit source = %q, want %q", h.Source, query.HistorySourceCurated)
		}

		if h.Status != model.ConversationStatusResolved {
			t.Errorf("curated hit status = %q, want resolved", h.Status)
		}
	}

	// Org-wide read (empty projectIDs): the B entry now appears too.
	orgHits, err := store.SearchKnowledge(t.Context(), orgID, nil, token, nil, 50)
	if err != nil {
		t.Fatalf("SearchKnowledge(org-wide): %v", err)
	}

	if !hitSet(orgHits)[bEntry.ID] {
		t.Error("org-wide curated read must include the project-B entry")
	}

	// pg_trgm fuzzy arm: a mistyped tag still hits.
	fuzzy, err := store.SearchKnowledge(t.Context(), orgID, []uuid.UUID{projA}, "authservce", nil, 50)
	if err != nil {
		t.Fatalf("SearchKnowledge(fuzzy): %v", err)
	}

	if len(fuzzy) == 0 {
		t.Error("fuzzy curated query matched nothing — pg_trgm arm not wired")
	}

	// Mark the A entry stale: it drops out of search (only active surfaces).
	if err := store.MarkStale(t.Context(), aEntry.ID); err != nil {
		t.Fatalf("MarkStale: %v", err)
	}

	afterStale, err := store.SearchKnowledge(t.Context(), orgID, []uuid.UUID{projA}, token, nil, 50)
	if err != nil {
		t.Fatalf("SearchKnowledge after stale: %v", err)
	}

	if hitSet(afterStale)[aEntry.ID] {
		t.Error("a stale curated entry must not appear in search")
	}
}

// TestListEntriesEveryStatus proves the dashboard list returns entries of every
// status in scope (unlike search, which is active-only).
func TestListEntriesEveryStatus(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := knowledge.NewStore(pool)

	orgID := testsupport.SeedOrganization(t, pool)
	proj := testsupport.SeedProjectInOrg(t, pool, orgID)
	author := testsupport.SeedMember(t, pool)

	active := seedEntry(t, store, orgID, proj, author, "active q", "a", "", nil, nil)
	staleEntry := seedEntry(t, store, orgID, proj, author, "stale q", "s", "", nil, nil)

	if err := store.MarkStale(t.Context(), staleEntry.ID); err != nil {
		t.Fatalf("MarkStale: %v", err)
	}

	entries, err := store.ListEntries(t.Context(), orgID, nil)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}

	seen := map[uuid.UUID]string{}
	for _, e := range entries {
		seen[e.ID] = e.Status
	}

	if seen[active.ID] != string(model.KnowledgeStatusActive) {
		t.Errorf("active entry missing/wrong status in list: %q", seen[active.ID])
	}

	if seen[staleEntry.ID] != string(model.KnowledgeStatusStale) {
		t.Errorf("stale entry must still appear in the dashboard list: %q", seen[staleEntry.ID])
	}
}

// seedSuggestion creates an agent-suggested (pending) entry and returns it.
func seedSuggestion(
	t *testing.T,
	store *knowledge.Store,
	orgID, projectID, authorID uuid.UUID,
	question, answer, summary string,
	tags, entities []string,
) model.KnowledgeEntry {
	t.Helper()

	entry, err := model.NewSuggestedKnowledgeEntry(uuid.New(), orgID, projectID, authorID, question, answer, summary, tags, entities)
	if err != nil {
		t.Fatalf("NewSuggestedKnowledgeEntry: %v", err)
	}

	stored, err := store.CreateEntry(t.Context(), entry)
	if err != nil {
		t.Fatalf("CreateEntry(suggestion): %v", err)
	}

	if stored.Source != model.KnowledgeSourceAgentSuggested || stored.Status != model.KnowledgeStatusPending {
		t.Fatalf("suggestion defaults wrong: source=%q status=%q", stored.Source, stored.Status)
	}

	return stored
}

// TestSuggestionApprovalQueue proves the Phase 2 trust gradient at the store
// level: an agent-suggested entry lands pending — NOT searchable, but visible in
// the pending queue; Activate (approve) makes it searchable and drops it from the
// queue; DeleteEntry (dismiss) removes it entirely.
func TestSuggestionApprovalQueue(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := knowledge.NewStore(pool)

	orgID := testsupport.SeedOrganization(t, pool)
	proj := testsupport.SeedProjectInOrg(t, pool, orgID)
	author := testsupport.SeedMember(t, pool)

	token := "zqxsugg" + uuid.NewString()[:8]

	pending := seedSuggestion(t, store, orgID, proj, author,
		"How does "+token+" work?", "The suggested answer.", token+" gist",
		[]string{"authservice"}, nil)

	// Pending is NOT searchable (search returns only active).
	hits, err := store.SearchKnowledge(t.Context(), orgID, []uuid.UUID{proj}, token, nil, 50)
	if err != nil {
		t.Fatalf("SearchKnowledge: %v", err)
	}

	if hitSet(hits)[pending.ID] {
		t.Fatal("a pending agent-suggested entry must NOT appear in search until approved")
	}

	// It IS in the pending-review queue.
	queue, err := store.ListPending(t.Context(), orgID, nil)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}

	if !viewSet(queue)[pending.ID] {
		t.Fatal("a pending agent-suggested entry must appear in the review queue")
	}

	// Approve → Activate: now searchable, gone from the queue, source preserved.
	if err := store.Activate(t.Context(), pending.ID); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	approved, err := store.EntryByID(t.Context(), pending.ID)
	if err != nil {
		t.Fatalf("EntryByID after activate: %v", err)
	}

	if approved.Status != model.KnowledgeStatusActive || approved.Source != model.KnowledgeSourceAgentSuggested {
		t.Errorf("approve should activate and keep provenance: status=%q source=%q", approved.Status, approved.Source)
	}

	afterHits, err := store.SearchKnowledge(t.Context(), orgID, []uuid.UUID{proj}, token, nil, 50)
	if err != nil {
		t.Fatalf("SearchKnowledge after approve: %v", err)
	}

	if !hitSet(afterHits)[pending.ID] {
		t.Error("an approved (active) suggestion must be searchable")
	}

	if viewSet(mustListPending(t, store, orgID))[pending.ID] {
		t.Error("an approved suggestion must drop out of the pending queue")
	}

	// Dismiss another pending suggestion → deleted entirely.
	toDismiss := seedSuggestion(t, store, orgID, proj, author, "dismiss me", "nope", "", nil, nil)

	if err := store.DeleteEntry(t.Context(), toDismiss.ID); err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}

	if _, err := store.EntryByID(t.Context(), toDismiss.ID); err == nil {
		t.Error("a dismissed suggestion must be deleted (EntryByID should fail)")
	}
}

// TestRevalidateReactivatesStale proves the light staleness workflow: MarkStale
// hides an entry from search, Activate (revalidate) brings it back.
func TestRevalidateReactivatesStale(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := knowledge.NewStore(pool)

	orgID := testsupport.SeedOrganization(t, pool)
	proj := testsupport.SeedProjectInOrg(t, pool, orgID)
	author := testsupport.SeedMember(t, pool)

	token := "zqxreval" + uuid.NewString()[:8]

	entry := seedEntry(t, store, orgID, proj, author,
		"How to "+token+"?", "answer", token+" gist", []string{"ops"}, nil)

	if err := store.MarkStale(t.Context(), entry.ID); err != nil {
		t.Fatalf("MarkStale: %v", err)
	}

	staleHits, err := store.SearchKnowledge(t.Context(), orgID, []uuid.UUID{proj}, token, nil, 50)
	if err != nil {
		t.Fatalf("SearchKnowledge(stale): %v", err)
	}

	if hitSet(staleHits)[entry.ID] {
		t.Fatal("a stale entry must not be searchable")
	}

	if err := store.Activate(t.Context(), entry.ID); err != nil {
		t.Fatalf("Activate(revalidate): %v", err)
	}

	revalidated, err := store.EntryByID(t.Context(), entry.ID)
	if err != nil {
		t.Fatalf("EntryByID after revalidate: %v", err)
	}

	if revalidated.Status != model.KnowledgeStatusActive {
		t.Errorf("revalidate should flip stale→active, got %q", revalidated.Status)
	}

	backHits, err := store.SearchKnowledge(t.Context(), orgID, []uuid.UUID{proj}, token, nil, 50)
	if err != nil {
		t.Fatalf("SearchKnowledge after revalidate: %v", err)
	}

	if !hitSet(backHits)[entry.ID] {
		t.Error("a revalidated entry must be searchable again")
	}
}

// mustListPending is a ListPending helper that fails the test on error.
func mustListPending(t *testing.T, store *knowledge.Store, orgID uuid.UUID) []query.KnowledgeEntryView {
	t.Helper()

	views, err := store.ListPending(t.Context(), orgID, nil)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}

	return views
}

// viewSet collapses a view slice into a presence set keyed by id.
func viewSet(views []query.KnowledgeEntryView) map[uuid.UUID]bool {
	out := make(map[uuid.UUID]bool, len(views))
	for _, v := range views {
		out[v.ID] = true
	}

	return out
}

// hitSet collapses a hit slice into a presence set keyed by id.
func hitSet(hits []query.HistoryHit) map[uuid.UUID]bool {
	out := make(map[uuid.UUID]bool, len(hits))
	for _, h := range hits {
		out[h.ConversationID] = true
	}

	return out
}
