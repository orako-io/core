// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// defaultTopK is the number of hits returned when a query does not specify one.
const defaultTopK = 10

// HistorySource labels where a HistoryHit came from. It is a trust/attribution
// hint for the UI ("Answered by {person} · {date}" vs "Curated by {org}"), NOT
// something the searching agent must branch on — a curated hit fills the same
// struct as an organic one and reads as a reusable resolved answer.
const (
	// HistorySourceConversation is an organic resolved/answered conversation
	// (a real person answered). The zero value; empty is treated as this.
	HistorySourceConversation = "conversation"
	// HistorySourceCurated is a human-authored curated knowledge entry.
	HistorySourceCurated = "curated"
)

// HistoryHit is the read-side DTO for one conversation matched by the
// deterministic history search. Unlike the retired SearchHit it carries NO
// confidence/contested signals — those were kb_entries concepts and are omitted
// from the new engine by design; the agent branches on Status instead. It
// exposes every field the agent needs to route (join an open thread, reuse a
// resolved answer, re-ask a timed-out/dismissed asker) without a second fetch;
// participants are resolved separately via get_conversation.
type HistoryHit struct {
	ConversationID uuid.UUID
	ProjectID      uuid.UUID
	// ProjectName is the resolved name of ProjectID, so a cross-project search
	// can attribute each hit.
	ProjectName string
	// Title is the display title (agent-written, falling back to a truncated
	// question when unset).
	Title string
	// Summary is the agent-curated one-line gist (empty for a conversation that
	// predates the metadata or was never curated).
	Summary string
	Status  model.ConversationStatus
	// Tags/Entities are the normalized facets the hit was (partly) matched on.
	Tags     []string
	Entities []string
	// AskerMemberID is who opened the conversation — the lead to re-ask when the
	// hit is a timed-out/dismissed (unanswered) question. uuid.Nil for a curated
	// hit (there is no asker to re-ask).
	AskerMemberID uuid.UUID
	// Source is the hit's provenance: "conversation" (organic history) or
	// "curated" (an authored knowledge entry). Empty is treated as
	// "conversation". A UI attribution/trust label only — the agent ignores it.
	Source string `exhaustruct:"optional"`
	// AuthorMemberID is who curated the entry, for a curated hit's attribution
	// ("Curated by …"); uuid.Nil for an organic hit or an unattributed entry.
	AuthorMemberID uuid.UUID `exhaustruct:"optional"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// Score is the deterministic relevance score (see HistoryReader.SearchHistory
	// for the formula); higher is more relevant. Recent-history browse hits use zero.
	Score float32
	// AgeDays is CreatedAt's age in whole days at query time — a convenience the
	// handler fills so the agent can weigh staleness without re-deriving it.
	AgeDays int `exhaustruct:"optional"`
}

// SearchHistoryQuery is the input for the deterministic (embedding-free) history
// search over conversations.
type SearchHistoryQuery struct {
	// ProjectIDs scopes the search. Empty falls back to every non-archived
	// project in OrgID the caller may see (resolved by the transport before this
	// query runs); a non-empty slice restricts to exactly those projects.
	// Archived projects are always excluded.
	ProjectIDs []uuid.UUID `exhaustruct:"optional"`
	// OrgID is the caller's organization, the org-wide fallback scope when
	// ProjectIDs is empty. uuid.Nil disables the org filter (self-host /
	// unresolved org).
	OrgID uuid.UUID `exhaustruct:"optional"`
	// Query is the free-text search string (matched by FTS + trgm fuzzy).
	Query string `exhaustruct:"optional"`
	// Tags are explicit facet terms to overlap against the conversation's
	// tags/entities — lets a hit surface on a named tag even without a lexical
	// match. Optional.
	Tags []string `exhaustruct:"optional"`
	// TopK is the maximum number of hits (default: defaultTopK).
	TopK int `exhaustruct:"optional"`
	// Status, when set to a concrete conversation status ("open", "answered",
	// "resolved", "timed_out", "dismissed"), filters the returned hits to that
	// status. Empty or "all" applies no filter. Optional; the search engine
	// itself is status-blind, so the handler applies this after fetching.
	Status string `exhaustruct:"optional"`
	// Browse requests the recent-conversations fallback (dashboard History tab):
	// when set and both Query and Tags are empty, the handler lists the most
	// recent conversations in scope instead of returning nothing. The agent
	// (MCP) never sets it, so its "no lexical/facet match = no hit" contract is
	// unchanged. Optional.
	Browse bool `exhaustruct:"optional"`
	// IncludeCurated unions ACTIVE curated knowledge entries into the results,
	// ranked alongside organic conversations by the same score, so the agent sees
	// both under one shape. The MCP search sets it; the dashboard History tab
	// leaves it false so its conversation-only view is unchanged. No-op when the
	// handler was built without a curated reader. Optional.
	IncludeCurated bool `exhaustruct:"optional"`
}

// HistoryReader is the read-side port for the deterministic history search.
// Satisfied by *conversation.Store. It has ZERO dependency on the embedder,
// reranker, or pgvector — the whole point of P3.
type HistoryReader interface {
	// SearchHistory returns the top-k conversations matching the query across
	// projectIDs, ALL statuses, ranked by a single server-side deterministic
	// ordering (FTS rank + facet-overlap boost + recency). An empty projectIDs
	// falls back to every non-archived project in orgID; archived projects are
	// always excluded.
	SearchHistory(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID, queryText string, tags []string, topK int) ([]HistoryHit, error)
	// RecentHistory returns up to topK most-recent conversations in the same
	// org/project scope as SearchHistory (archived projects excluded), newest
	// first, optionally narrowed to a single status. It backs the History tab's
	// no-query browse: no FTS match required. status "" (or "all") = all statuses.
	RecentHistory(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID, status string, topK int) ([]HistoryHit, error)
	// HistoryStatusCounts returns the per-status conversation breakdown over the
	// same scope (never narrowed by a status filter), for the KPI/facet pills.
	HistoryStatusCounts(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID) (HistoryStatusCounts, error)
}

// HistoryStatusCounts is the per-status conversation breakdown for one scope:
// the KPI/facet pill numbers behind the History tab. All is the scope total;
// TimedOut is the "expired" bucket; "leads without an answer" = TimedOut +
// Dismissed.
type HistoryStatusCounts struct {
	All       int
	Resolved  int
	Answered  int
	Open      int
	TimedOut  int
	Dismissed int
}

// CuratedReader is the read-side port for the curated-knowledge arm of the
// search union. Satisfied by *knowledge.Store. Optional on the handler: when
// unset, IncludeCurated is a no-op and only conversations are searched.
type CuratedReader interface {
	// SearchKnowledge returns the top-k ACTIVE curated entries matching the query
	// in scope (org + org-wide + optional project set), shaped as HistoryHit with
	// Source "curated" and ranked by the SAME score formula as SearchHistory so
	// the two lists merge into one comparable ranking.
	SearchKnowledge(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID, queryText string, tags []string, topK int) ([]HistoryHit, error)
}

// SearchHistoryHandler handles SearchHistoryQuery. It holds no embedder — the
// read side is pure DB.
type SearchHistoryHandler struct {
	reader HistoryReader
	// curated is the optional curated-knowledge arm of the search union; nil
	// disables it (dashboard/self-host wiring may omit it). Set via WithCurated.
	curated CuratedReader `exhaustruct:"optional"`
}

// MustNewSearchHistoryHandler builds a handler. It panics on a nil
// dependency.
func MustNewSearchHistoryHandler(reader HistoryReader) SearchHistoryHandler {
	if reader == nil {
		panic("SearchHistoryHandler requires a non-nil HistoryReader")
	}

	return SearchHistoryHandler{reader: reader, curated: nil}
}

// WithCurated returns a copy of the handler that also searches curated
// knowledge when a query sets IncludeCurated. A nil reader is ignored (leaves
// the union disabled).
func (h SearchHistoryHandler) WithCurated(curated CuratedReader) SearchHistoryHandler {
	h.curated = curated

	return h
}

// Handle runs the deterministic history search and returns the ranked hits with
// AgeDays filled in. With Browse set and no query/tags it lists recent
// conversations instead (History-tab browse); a Status other than "" / "all"
// narrows the returned hits to that status.
func (h SearchHistoryHandler) Handle(ctx context.Context, q SearchHistoryQuery) ([]HistoryHit, error) {
	topK := q.TopK
	if topK <= 0 {
		topK = defaultTopK
	}

	status := normalizeStatusFilter(q.Status)

	var (
		hits []HistoryHit
		err  error
	)

	if q.Browse && strings.TrimSpace(q.Query) == "" && len(q.Tags) == 0 {
		// No search terms: browse the most recent conversations in scope. The
		// status filter is pushed into the SQL here (exact, not capped by topK).
		hits, err = h.reader.RecentHistory(ctx, q.OrgID, q.ProjectIDs, status, topK)
		if err != nil {
			return nil, errs.InternalError{Err: fmt.Errorf("browsing history: %w", err)}
		}

		return withAgeDays(hits), nil
	}

	hits, err = h.reader.SearchHistory(ctx, q.OrgID, q.ProjectIDs, q.Query, q.Tags, topK)
	if err != nil {
		return nil, errs.InternalError{Err: fmt.Errorf("searching history: %w", err)}
	}

	// Union in curated knowledge when asked (MCP path). Both arms rank by the
	// same score formula, so the merged list re-sorts by score and re-caps at
	// topK — the agent sees organic and curated hits under one shape.
	if q.IncludeCurated && h.curated != nil {
		curated, cerr := h.curated.SearchKnowledge(ctx, q.OrgID, q.ProjectIDs, q.Query, q.Tags, topK)
		if cerr != nil {
			return nil, errs.InternalError{Err: fmt.Errorf("searching curated knowledge: %w", cerr)}
		}

		hits = mergeRankedHits(hits, curated, topK)
	}

	// The search engine is status-blind by design, so a status facet is applied
	// after ranking. The KPI/facet counts come from HistoryStatusCounts (exact),
	// so this only trims the visible card list. Curated hits are always
	// "resolved", so a status filter simply excludes them unless resolved is
	// requested — the desired behavior for a conversation-status facet.
	if status != "" {
		hits = filterByStatus(hits, status)
	}

	return withAgeDays(hits), nil
}

// mergeRankedHits concatenates two independently-ranked hit lists and re-sorts
// them by score (descending), breaking ties by recency (newest first), then
// caps the result at topK. Both inputs must be ranked by the same score formula
// for the merge to be meaningful. Returns a non-nil slice.
func mergeRankedHits(organic, curated []HistoryHit, topK int) []HistoryHit {
	merged := make([]HistoryHit, 0, len(organic)+len(curated))
	merged = append(merged, organic...)
	merged = append(merged, curated...)

	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Score != merged[j].Score {
			return merged[i].Score > merged[j].Score
		}

		return merged[i].CreatedAt.After(merged[j].CreatedAt)
	})

	if topK > 0 && len(merged) > topK {
		merged = merged[:topK]
	}

	return merged
}

// normalizeStatusFilter lower-cases the requested status and treats "" / "all"
// as "no filter" (returns "").
func normalizeStatusFilter(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	if s == "" || s == "all" {
		return ""
	}

	return s
}

// filterByStatus keeps only the hits whose status equals want. It preserves
// order and always returns a non-nil slice.
func filterByStatus(hits []HistoryHit, want string) []HistoryHit {
	out := make([]HistoryHit, 0, len(hits))

	for _, hit := range hits {
		if string(hit.Status) == want {
			out = append(out, hit)
		}
	}

	return out
}

// HistoryStatusCountsQuery asks for the per-status conversation breakdown over
// a scope (org + optional project set), independent of any search terms.
type HistoryStatusCountsQuery struct {
	// ProjectIDs scopes the counts; empty falls back to every non-archived
	// project in OrgID the caller may see (same rule as SearchHistoryQuery).
	ProjectIDs []uuid.UUID `exhaustruct:"optional"`
	// OrgID is the caller's organization (the org-wide fallback scope).
	OrgID uuid.UUID `exhaustruct:"optional"`
}

// HistoryStatusCountsHandler handles HistoryStatusCountsQuery. Read side only.
type HistoryStatusCountsHandler struct {
	reader HistoryReader
}

// MustNewHistoryStatusCountsHandler builds a handler. It panics on a
// nil dependency.
func MustNewHistoryStatusCountsHandler(reader HistoryReader) HistoryStatusCountsHandler {
	if reader == nil {
		panic("HistoryStatusCountsHandler requires a non-nil HistoryReader")
	}

	return HistoryStatusCountsHandler{reader: reader}
}

// Handle returns the per-status conversation breakdown for the scope.
func (h HistoryStatusCountsHandler) Handle(ctx context.Context, q HistoryStatusCountsQuery) (HistoryStatusCounts, error) {
	counts, err := h.reader.HistoryStatusCounts(ctx, q.OrgID, q.ProjectIDs)
	if err != nil {
		return HistoryStatusCounts{}, errs.InternalError{Err: fmt.Errorf("counting history statuses: %w", err)}
	}

	return counts, nil
}

// withAgeDays fills each hit's AgeDays from its CreatedAt at call time. It
// mutates and returns the same slice (the reader hands back a fresh slice).
func withAgeDays(hits []HistoryHit) []HistoryHit {
	now := time.Now().UTC()
	for i := range hits {
		age := max(now.Sub(hits[i].CreatedAt), 0)
		hits[i].AgeDays = int(age / (24 * time.Hour))
	}

	return hits
}
