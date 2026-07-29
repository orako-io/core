// SPDX-License-Identifier: AGPL-3.0-or-later

// Package knowledge is the Postgres adapter for curated knowledge entries
// (decision 2026-07-26): a dedicated table, searched with the SAME FTS +
// pg_trgm engine and ranking formula as conversation history so a curated hit
// is indistinguishable from an organic one to a searching agent. It holds no
// embedder — the read side is pure DB (upholds the 2026-07-24 decision).
package knowledge

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/pkg/pgconv"
	postgres "github.com/orako-io/core/internal/pkg/postgres"
)

// knowledgeTrgmThreshold is the pg_trgm similarity cutoff above which a fuzzy
// label match counts as a hit — identical to the conversation history search
// (pg_trgm's own 0.3 default), so ranking is comparable across the two sources.
const knowledgeTrgmThreshold = 0.3

// searchKnowledgeSQL is the deterministic, embedding-free search over ACTIVE
// curated entries. It mirrors the conversation history search (searchHistorySQL
// in the conversation adapter) arm-for-arm and, crucially, TERM-for-TERM in the
// score expression, so curated and organic hits carry comparable scores and can
// be merged into one ranked list.
//
// Scope: the caller's org ($1), plus every org-wide entry (project_id IS NULL);
// a non-empty project set ($2) additionally admits entries scoped to those
// projects. An empty $2 (org-wide read) admits all the org's entries. $1 =
// NilOrgID disables the org filter for the self-host / unresolved-org case.
//
//	score = ts_rank(fts)                          -- lexical relevance
//	      + 0.5 * similarity(labels, query)       -- fuzzy facet closeness
//	      + 0.3 * (tag/entity overlaps $4 ? 1 : 0) -- explicit facet boost
//	      + 0.1 * exp(-age_seconds / 30d)          -- recency (newer ranked higher)
const searchKnowledgeSQL = `
SELECT k.id, k.project_id, p.name, k.question, k.answer, k.summary,
       k.tags, k.entities, k.author_member_id, k.created_at, k.updated_at,
       (
         ts_rank(k.fts, websearch_to_tsquery('simple', $3))
         + 0.5 * similarity(k.search_labels, $3)
         + 0.3 * (CASE WHEN k.tags && $4::text[] OR k.entities && $4::text[] THEN 1.0 ELSE 0.0 END)
         + 0.1 * exp(- extract(epoch FROM (now() - k.created_at)) / 2592000.0)
       )::real AS score
FROM knowledge_entries k
LEFT JOIN projects p ON p.id = k.project_id
WHERE (k.org_id = $1 OR $1 = '` + pgconv.NilOrgID + `'::uuid)
  AND k.status = 'active'
  AND (
        k.project_id IS NULL
     OR cardinality($2::uuid[]) = 0
     OR k.project_id = ANY($2::uuid[])
  )
  AND (
        k.fts @@ websearch_to_tsquery('simple', $3)
     OR similarity(k.search_labels, $3) > $5
     OR k.tags && $4::text[]
     OR k.entities && $4::text[]
  )
ORDER BY score DESC, k.created_at DESC
LIMIT $6
`

// listKnowledgeSQL lists curated entries in scope newest first, for the
// dashboard Knowledge section. It returns every status (the UI shows and can
// re-activate stale entries), unlike the search which only surfaces active.
// Scope matches searchKnowledgeSQL (org + org-wide + optional project set).
const listKnowledgeSQL = `
SELECT k.id, k.org_id, k.project_id, p.name, k.question, k.answer, k.summary,
       k.tags, k.entities, k.author_member_id, k.source, k.status,
       k.created_at, k.updated_at
FROM knowledge_entries k
LEFT JOIN projects p ON p.id = k.project_id
WHERE (k.org_id = $1 OR $1 = '` + pgconv.NilOrgID + `'::uuid)
  AND (
        k.project_id IS NULL
     OR cardinality($2::uuid[]) = 0
     OR k.project_id = ANY($2::uuid[])
  )
ORDER BY k.created_at DESC
LIMIT $3
`

// listPendingKnowledgeSQL lists agent-suggested entries awaiting human review
// (source='agent_suggested' AND status='pending') in scope, newest first, for
// the dashboard's "Pending review" queue. Scope matches listKnowledgeSQL (org +
// org-wide + optional project set).
const listPendingKnowledgeSQL = `
SELECT k.id, k.org_id, k.project_id, p.name, k.question, k.answer, k.summary,
       k.tags, k.entities, k.author_member_id, k.source, k.status,
       k.created_at, k.updated_at
FROM knowledge_entries k
LEFT JOIN projects p ON p.id = k.project_id
WHERE (k.org_id = $1 OR $1 = '` + pgconv.NilOrgID + `'::uuid)
  AND k.source = 'agent_suggested'
  AND k.status = 'pending'
  AND (
        k.project_id IS NULL
     OR cardinality($2::uuid[]) = 0
     OR k.project_id = ANY($2::uuid[])
  )
ORDER BY k.created_at DESC
LIMIT $3
`

// listKnowledgeLimit caps the dashboard list read.
const listKnowledgeLimit = 500

var (
	_ query.CuratedReader          = (*Store)(nil)
	_ query.KnowledgeEntryReader   = (*Store)(nil)
	_ query.PendingKnowledgeReader = (*Store)(nil)
)

// Store is the Postgres-backed adapter for curated knowledge entries.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// CreateEntry durably stores a new curated entry. Returns
// adaptererr.ErrDuplicate when an entry with the same ID already exists.
func (s *Store) CreateEntry(ctx context.Context, e model.KnowledgeEntry) (model.KnowledgeEntry, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).createKnowledgeEntry(ctx, createKnowledgeEntryParams{
		ID:             e.ID,
		OrgID:          e.OrgID,
		ProjectID:      pgconv.UUIDOrNull(e.ProjectID),
		Question:       e.Question,
		Answer:         e.Answer,
		Summary:        e.Summary,
		Tags:           stringSliceOrEmpty(e.Tags),
		Entities:       stringSliceOrEmpty(e.Entities),
		AuthorMemberID: pgconv.UUIDOrNull(e.AuthorMemberID),
		Source:         string(e.Source),
		Status:         string(e.Status),
	})
	if err != nil {
		return model.KnowledgeEntry{}, fmt.Errorf("creating knowledge entry: %w", adaptererr.Decode(err))
	}

	return model.KnowledgeEntry{
		ID:             row.ID,
		OrgID:          row.OrgID,
		ProjectID:      pgconv.UUIDFromPgtype(row.ProjectID),
		Question:       row.Question,
		Answer:         row.Answer,
		Summary:        row.Summary,
		Tags:           row.Tags,
		Entities:       row.Entities,
		AuthorMemberID: pgconv.UUIDFromPgtype(row.AuthorMemberID),
		Source:         model.KnowledgeSource(row.Source),
		Status:         model.KnowledgeStatus(row.Status),
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}, nil
}

// EntryByID returns one entry. Returns adaptererr.ErrNotFound when absent.
func (s *Store) EntryByID(ctx context.Context, id uuid.UUID) (model.KnowledgeEntry, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).knowledgeEntryByID(ctx, id)
	if err != nil {
		return model.KnowledgeEntry{}, fmt.Errorf("reading knowledge entry: %w", adaptererr.Decode(err))
	}

	return model.KnowledgeEntry{
		ID:             row.ID,
		OrgID:          row.OrgID,
		ProjectID:      pgconv.UUIDFromPgtype(row.ProjectID),
		Question:       row.Question,
		Answer:         row.Answer,
		Summary:        row.Summary,
		Tags:           row.Tags,
		Entities:       row.Entities,
		AuthorMemberID: pgconv.UUIDFromPgtype(row.AuthorMemberID),
		Source:         model.KnowledgeSource(row.Source),
		Status:         model.KnowledgeStatus(row.Status),
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}, nil
}

// UpdateEntry rewrites the editable fields (question/answer/summary/tags/
// entities/status) of an existing entry and returns the updated row. Returns
// adaptererr.ErrNotFound when absent.
func (s *Store) UpdateEntry(ctx context.Context, e model.KnowledgeEntry) (model.KnowledgeEntry, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).updateKnowledgeEntry(ctx, updateKnowledgeEntryParams{
		ID:       e.ID,
		Question: e.Question,
		Answer:   e.Answer,
		Summary:  e.Summary,
		Tags:     stringSliceOrEmpty(e.Tags),
		Entities: stringSliceOrEmpty(e.Entities),
		Status:   string(e.Status),
	})
	if err != nil {
		return model.KnowledgeEntry{}, fmt.Errorf("updating knowledge entry: %w", adaptererr.Decode(err))
	}

	return model.KnowledgeEntry{
		ID:             row.ID,
		OrgID:          row.OrgID,
		ProjectID:      pgconv.UUIDFromPgtype(row.ProjectID),
		Question:       row.Question,
		Answer:         row.Answer,
		Summary:        row.Summary,
		Tags:           row.Tags,
		Entities:       row.Entities,
		AuthorMemberID: pgconv.UUIDFromPgtype(row.AuthorMemberID),
		Source:         model.KnowledgeSource(row.Source),
		Status:         model.KnowledgeStatus(row.Status),
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}, nil
}

// MarkStale flips an entry to status='stale' so it drops out of search. Returns
// adaptererr.ErrNotFound when absent.
func (s *Store) MarkStale(ctx context.Context, id uuid.UUID) error {
	_, err := New(postgres.Conn(ctx, s.pool)).markKnowledgeEntryStale(ctx, id)
	if err != nil {
		return fmt.Errorf("marking knowledge entry stale: %w", adaptererr.Decode(err))
	}

	return nil
}

// Activate flips an entry to status='active' and bumps updated_at ("last
// reviewed"). Shared by Approve (a vetted agent-suggested entry) and Revalidate
// (a re-confirmed stale entry). Returns adaptererr.ErrNotFound when absent.
func (s *Store) Activate(ctx context.Context, id uuid.UUID) error {
	_, err := New(postgres.Conn(ctx, s.pool)).activateKnowledgeEntry(ctx, id)
	if err != nil {
		return fmt.Errorf("activating knowledge entry: %w", adaptererr.Decode(err))
	}

	return nil
}

// DeleteEntry hard-deletes an entry (the Dismiss path for a pending suggestion —
// no 'dismissed' status is kept, so a rejected proposal simply disappears).
// Returns adaptererr.ErrNotFound when absent.
func (s *Store) DeleteEntry(ctx context.Context, id uuid.UUID) error {
	if err := New(postgres.Conn(ctx, s.pool)).deleteKnowledgeEntry(ctx, id); err != nil {
		return fmt.Errorf("deleting knowledge entry: %w", adaptererr.Decode(err))
	}

	return nil
}

// SearchKnowledge returns the top-k ACTIVE curated entries matching the query in
// scope, shaped as query.HistoryHit values with Source "curated" and Status
// "resolved" so a searching agent treats them exactly like a reusable resolved
// conversation. Returns an empty (non-nil) slice when nothing matches.
func (s *Store) SearchKnowledge(
	ctx context.Context,
	orgID uuid.UUID,
	projectIDs []uuid.UUID,
	queryText string,
	tags []string,
	topK int,
) ([]query.HistoryHit, error) {
	ids := projectIDs
	if ids == nil {
		ids = []uuid.UUID{}
	}

	rows, err := s.pool.Query(
		ctx, searchKnowledgeSQL,
		orgID, ids, queryText, stringSliceOrEmpty(tags), knowledgeTrgmThreshold, topK,
	)
	if err != nil {
		return nil, fmt.Errorf("searching knowledge: %w", adaptererr.Decode(err))
	}

	return scanKnowledgeHits(rows)
}

// scanKnowledgeHits drains a searchKnowledgeSQL result into HistoryHit values.
// The curated shape maps 1:1 onto the organic one: the entry id fills
// ConversationID (a stable opaque id), the question fills the display title, and
// the summary — or the answer when no summary was curated — fills the snippet,
// exactly as a resolved conversation's summary "doubles as the answer snippet".
func scanKnowledgeHits(rows pgx.Rows) ([]query.HistoryHit, error) {
	defer rows.Close()

	var results []query.HistoryHit

	for rows.Next() {
		var (
			hit       query.HistoryHit
			projectID pgtype.UUID
			project   pgtype.Text
			question  string
			answer    string
			summary   string
			author    pgtype.UUID
			score     float32
		)

		if err := rows.Scan(
			&hit.ConversationID,
			&projectID,
			&project,
			&question,
			&answer,
			&summary,
			&hit.Tags,
			&hit.Entities,
			&author,
			&hit.CreatedAt,
			&hit.UpdatedAt,
			&score,
		); err != nil {
			return nil, fmt.Errorf("scanning knowledge hit row: %w", err)
		}

		hit.ProjectID = pgconv.UUIDFromPgtype(projectID)
		hit.ProjectName = pgconv.StringFromText(project)
		hit.Title = model.DisplayTitle("", question)
		hit.Summary = knowledgeSnippet(summary, answer)
		hit.Status = model.ConversationStatusResolved
		hit.AuthorMemberID = pgconv.UUIDFromPgtype(author)
		hit.Source = query.HistorySourceCurated
		hit.Score = score

		results = append(results, hit)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating knowledge hits: %w", adaptererr.Decode(err))
	}

	if results == nil {
		return []query.HistoryHit{}, nil
	}

	return results, nil
}

// ListEntries returns curated entries in scope (every status), newest first,
// for the dashboard Knowledge section, as read-side views (the read side owns
// its DTO — decision 2026-07-20).
func (s *Store) ListEntries(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID) ([]query.KnowledgeEntryView, error) {
	ids := projectIDs
	if ids == nil {
		ids = []uuid.UUID{}
	}

	rows, err := s.pool.Query(ctx, listKnowledgeSQL, orgID, ids, listKnowledgeLimit)
	if err != nil {
		return nil, fmt.Errorf("listing knowledge entries: %w", adaptererr.Decode(err))
	}

	return scanKnowledgeViews(rows)
}

// ListPending returns the agent-suggested entries awaiting review (pending) in
// scope, newest first, as read-side views for the dashboard's "Pending review"
// queue. Same scope + view shape as ListEntries; only the source/status filter
// differs (the SQL owns it).
func (s *Store) ListPending(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID) ([]query.KnowledgeEntryView, error) {
	ids := projectIDs
	if ids == nil {
		ids = []uuid.UUID{}
	}

	rows, err := s.pool.Query(ctx, listPendingKnowledgeSQL, orgID, ids, listKnowledgeLimit)
	if err != nil {
		return nil, fmt.Errorf("listing pending knowledge entries: %w", adaptererr.Decode(err))
	}

	return scanKnowledgeViews(rows)
}

// scanKnowledgeViews drains a list-shaped result (listKnowledgeSQL /
// listPendingKnowledgeSQL — identical column order) into read-side views.
func scanKnowledgeViews(rows pgx.Rows) ([]query.KnowledgeEntryView, error) {
	defer rows.Close()

	var results []query.KnowledgeEntryView

	for rows.Next() {
		var (
			view      query.KnowledgeEntryView
			projectID pgtype.UUID
			project   pgtype.Text
			author    pgtype.UUID
		)

		if err := rows.Scan(
			&view.ID,
			&view.OrgID,
			&projectID,
			&project,
			&view.Question,
			&view.Answer,
			&view.Summary,
			&view.Tags,
			&view.Entities,
			&author,
			&view.Source,
			&view.Status,
			&view.CreatedAt,
			&view.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning knowledge entry row: %w", err)
		}

		view.ProjectID = pgconv.UUIDFromPgtype(projectID)
		view.ProjectName = pgconv.StringFromText(project)
		view.AuthorMemberID = pgconv.UUIDFromPgtype(author)

		results = append(results, view)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating knowledge entries: %w", adaptererr.Decode(err))
	}

	if results == nil {
		return []query.KnowledgeEntryView{}, nil
	}

	return results, nil
}

// knowledgeSnippet returns the curated summary, or the answer when no summary
// was authored — so a searching agent always receives the substantive answer
// text in the hit (a curated entry has no get_conversation thread to fetch).
func knowledgeSnippet(summary, answer string) string {
	if summary != "" {
		return summary
	}

	return answer
}

// stringSliceOrEmpty returns s, or an empty (non-nil) slice when s is nil, so a
// nil facet slice persists/searches as an empty text[] rather than SQL NULL.
func stringSliceOrEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}
