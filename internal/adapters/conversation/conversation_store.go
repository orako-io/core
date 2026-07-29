// SPDX-License-Identifier: AGPL-3.0-or-later

package conversation

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/pkg/pgconv"
	postgres "github.com/orako-io/core/internal/pkg/postgres"
)

// conversationsByScopeSQL lists conversations across a multi-project scope,
// newest first. projectIDs ($2) is a Postgres uuid[] parameter: an empty array
// falls back to every non-archived project in org_id ($1) — the org-wide read
// — while a non-empty array restricts to exactly those projects (already
// intersected with caller visibility by the transport). Archived projects are
// always excluded, even when explicitly named in projectIDs. ($3 = ” OR
// status = $3) covers both the filtered and unfiltered status paths without a
// dynamic query builder.
const conversationsByScopeSQL = `
SELECT c.id, c.project_id, c.asker_member_id, c.responder_member_id,
       c.status, c.question, c.title, c.context,
       c.created_at, c.updated_at,
       p.name
FROM conversations c
JOIN projects p ON p.id = c.project_id
WHERE p.archived_at IS NULL
  AND (p.org_id = $1 OR $1 = '` + pgconv.NilOrgID + `'::uuid)
  AND (cardinality($2::uuid[]) = 0 OR c.project_id = ANY($2::uuid[]))
  AND ($3 = '' OR c.status = $3)
ORDER BY c.created_at DESC
`

// conversationsByScopeForMemberSQL is the visibility-scoped variant of
// conversationsByScopeSQL: only conversations the caller ($4) asked or is the
// assigned responder for.
const conversationsByScopeForMemberSQL = `
SELECT c.id, c.project_id, c.asker_member_id, c.responder_member_id,
       c.status, c.question, c.title, c.context,
       c.created_at, c.updated_at,
       p.name
FROM conversations c
JOIN projects p ON p.id = c.project_id
WHERE p.archived_at IS NULL
  AND (p.org_id = $1 OR $1 = '` + pgconv.NilOrgID + `'::uuid)
  AND (cardinality($2::uuid[]) = 0 OR c.project_id = ANY($2::uuid[]))
  AND ($3 = '' OR c.status = $3)
  AND (c.asker_member_id = $4 OR c.responder_member_id = $4)
ORDER BY c.created_at DESC
`

// openConversationsByOrgSQL is the org-admin inbox bypass: every open
// conversation in any project owned by the org, newest first.
const openConversationsByOrgSQL = `
SELECT c.id, c.project_id, c.asker_member_id, c.responder_member_id,
       c.status, c.question, c.title, c.context,
       c.created_at, c.updated_at
FROM conversations c
JOIN projects p ON p.id = c.project_id
WHERE p.org_id = $1
  AND c.status = 'open'
ORDER BY c.created_at DESC
`

// distinctTagsByScopeSQL returns the distinct tags curated across the
// conversations of the given projects, most-frequent first (ties broken
// alphabetically), capped at $2. Archived projects are excluded; an empty
// project scope ($1) matches nothing. LATERAL unnest expands each row's tags[]
// so they can be grouped and counted. Hand-written pgx (not sqlc) for the same
// reason the scope reads above are: the uuid[] scope does not fit sqlc's
// named-query model.
const distinctTagsByScopeSQL = `
SELECT tag
FROM conversations c
JOIN projects p ON p.id = c.project_id
CROSS JOIN LATERAL unnest(c.tags) AS tag
WHERE p.archived_at IS NULL
  AND c.project_id = ANY($1::uuid[])
GROUP BY tag
ORDER BY COUNT(*) DESC, tag ASC
LIMIT $2
`

// distinctEntitiesByScopeSQL is distinctTagsByScopeSQL for the entities column.
const distinctEntitiesByScopeSQL = `
SELECT entity
FROM conversations c
JOIN projects p ON p.id = c.project_id
CROSS JOIN LATERAL unnest(c.entities) AS entity
WHERE p.archived_at IS NULL
  AND c.project_id = ANY($1::uuid[])
GROUP BY entity
ORDER BY COUNT(*) DESC, entity ASC
LIMIT $2
`

// historyTrgmThreshold is the pg_trgm similarity cutoff (0..1) above which a
// fuzzy label match counts as a hit. 0.3 is pg_trgm's own default
// (pg_trgm.similarity_threshold) — high enough to reject noise, low enough to
// rescue typos/short synonyms of an existing tag/entity.
const historyTrgmThreshold = 0.3

// historyHitColumns is the shared SELECT projection for every history-search
// read: the fields a HistoryHit carries plus the raw question (resolved into the
// display title in scanHistoryHits). The trailing score expression differs per
// query, so it is appended by each statement, not baked in here.
const historyHitColumns = `c.id, c.project_id, p.name, c.title, c.question, c.summary,
	       c.status, c.tags, c.entities, c.asker_member_id, c.created_at, c.updated_at`

// searchHistorySQL is the deterministic, embedding-free history search over
// conversations. Scope CTE mirrors ConversationsByProjectIDs
// exactly: org + optional projectIDs, archived projects always excluded, empty
// projectIDs ($2) falling back to the org-wide read.
//
// Match (any arm): FTS over summary+question+title+tags+entities, OR pg_trgm
// fuzzy similarity over the tag/entity labels above the threshold, OR a direct
// tag/entity overlap with the caller-supplied $4 facet terms — so a query that
// names an existing tag still hits even with no lexical FTS match.
//
// Ranking (deterministic, explainable — a SINGLE server-side ordering, never
// concatenated per project):
//
//	score = ts_rank(fts)                          -- lexical relevance
//	      + 0.5 * similarity(labels, query)       -- fuzzy facet closeness
//	      + 0.3 * (tag/entity overlaps $4 ? 1 : 0) -- explicit facet boost
//	      + 0.1 * exp(-age_seconds / 30d)          -- recency (newer ranked higher)
//
// Deliberately carries NO confidence/contested term: those were kb_entries
// concepts removed with the old engine; the agent branches on status instead.
// ALL statuses are eligible — no status filter — so the caller can decide
// whether to join an open thread, reuse a resolved answer, or re-ask an
// unanswered (timed_out/dismissed) question.
const searchHistorySQL = `
WITH scope AS (
	SELECT p.id
	FROM projects p
	WHERE p.archived_at IS NULL
	  AND (p.org_id = $1 OR $1 = '` + pgconv.NilOrgID + `'::uuid)
	  AND (cardinality($2::uuid[]) = 0 OR p.id = ANY($2::uuid[]))
)
SELECT ` + historyHitColumns + `,
       (
         ts_rank(c.fts, websearch_to_tsquery('simple', $3))
         + 0.5 * similarity(c.search_labels, $3)
         + 0.3 * (CASE WHEN c.tags && $4::text[] OR c.entities && $4::text[] THEN 1.0 ELSE 0.0 END)
         + 0.1 * exp(- extract(epoch FROM (now() - c.created_at)) / 2592000.0)
       )::real AS score
FROM conversations c
JOIN projects p ON p.id = c.project_id
WHERE c.project_id IN (SELECT id FROM scope)
  AND (
        c.fts @@ websearch_to_tsquery('simple', $3)
     OR similarity(c.search_labels, $3) > $5
     OR c.tags && $4::text[]
     OR c.entities && $4::text[]
  )
ORDER BY score DESC, c.created_at DESC
LIMIT $6
`

// recentHistorySQL browses the most-recent conversations in scope with NO
// lexical/facet match required — the History tab's empty-query default. Scope
// CTE mirrors searchHistorySQL exactly (org + optional projectIDs, archived
// projects excluded, empty projectIDs falling back to the org-wide read). $3 is
// an optional single-status filter (” = every status). score is a constant 0
// (there is no relevance to rank on); ordering is pure recency.
const recentHistorySQL = `
WITH scope AS (
	SELECT p.id
	FROM projects p
	WHERE p.archived_at IS NULL
	  AND (p.org_id = $1 OR $1 = '` + pgconv.NilOrgID + `'::uuid)
	  AND (cardinality($2::uuid[]) = 0 OR p.id = ANY($2::uuid[]))
)
SELECT ` + historyHitColumns + `,
       0::real AS score
FROM conversations c
JOIN projects p ON p.id = c.project_id
WHERE c.project_id IN (SELECT id FROM scope)
  AND ($3 = '' OR c.status = $3)
ORDER BY c.created_at DESC
LIMIT $4
`

// historyStatusCountsSQL is the grouped per-status conversation tally over the
// same scope CTE as searchHistorySQL — one row per present status, backing the
// History tab's KPI and facet pills. A status with zero conversations is simply
// absent (the caller zero-fills).
const historyStatusCountsSQL = `
WITH scope AS (
	SELECT p.id
	FROM projects p
	WHERE p.archived_at IS NULL
	  AND (p.org_id = $1 OR $1 = '` + pgconv.NilOrgID + `'::uuid)
	  AND (cardinality($2::uuid[]) = 0 OR p.id = ANY($2::uuid[]))
)
SELECT c.status, count(*)
FROM conversations c
WHERE c.project_id IN (SELECT id FROM scope)
GROUP BY c.status
`

// inboxByMemberSQL selects the open conversations addressed to a responder,
// newest first (the dashboard inbox read model).
const inboxByMemberSQL = `
SELECT id, project_id, asker_member_id, responder_member_id,
       status, question, title, context,
       created_at, updated_at
FROM conversations
WHERE responder_member_id = $1
  AND status = 'open'
ORDER BY created_at DESC
`

var _ repository.ConversationRepository = (*Store)(nil)

var _ query.VocabularyReader = (*Store)(nil)

var _ query.HistoryReader = (*Store)(nil)

// BlobDeleter removes attachment bytes from object storage. service.BlobStore
// (the S3-compatible blob adapter, or the disabled no-op) satisfies it. It is
// optional on the Store: when nil or reporting !Enabled, DeleteConversation
// skips blob cleanup (a metadata-only deployment with no object storage wired).
type BlobDeleter interface {
	Delete(ctx context.Context, key string) error
	Enabled() bool
}

// Store is the Postgres-backed ConversationRepository.
type Store struct {
	pool *pgxpool.Pool
	// blobs deletes a conversation's attachment bytes on hard-delete. Optional
	// (nil-safe); set via WithBlobDeleter at wiring time.
	blobs  BlobDeleter
	logger *slog.Logger
}

// NewStore builds a Store backed by pool. Attach a blob deleter with
// WithBlobDeleter so DeleteConversation also removes attachment bytes.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, blobs: nil, logger: slog.Default()}
}

// WithBlobDeleter wires the object-store deleter used to remove a conversation's
// attachment bytes when it is hard-deleted, and returns the Store for chaining.
// Passing nil leaves blob cleanup disabled (the DB rows still cascade).
func (s *Store) WithBlobDeleter(blobs BlobDeleter) *Store {
	s.blobs = blobs

	return s
}

// CreateConversation durably stores a new conversation.
// Returns adaptererr.ErrDuplicate when a conversation with the same ID already exists.
func (s *Store) CreateConversation(ctx context.Context, conv model.Conversation) error {
	_, err := New(postgres.Conn(ctx, s.pool)).createConversation(ctx, newCreateConversationParams(conv))
	if err != nil {
		return fmt.Errorf("creating conversation: %w", adaptererr.Decode(err))
	}

	return nil
}

// newCreateConversationParams maps a conversation aggregate to the sqlc insert
// params, defaulting the NOT NULL facet arrays to '{}'. Shared by the plain
// create path and the transactional OpenConversation path.
func newCreateConversationParams(conv model.Conversation) createConversationParams {
	return createConversationParams{
		ID:                conv.ID,
		ProjectID:         conv.ProjectID,
		AskerMemberID:     conv.AskerMemberID,
		ResponderMemberID: pgconv.UUIDOrNull(conv.ResponderMemberID),
		Status:            string(conv.Status),
		Question:          conv.Question,
		Title:             conv.Title,
		Context:           conv.Context,
		Summary:           conv.Summary,
		Tags:              stringSliceOrEmpty(conv.Tags),
		Entities:          stringSliceOrEmpty(conv.Entities),
	}
}

// ConversationByID fetches a conversation by its UUID.
// Returns adaptererr.ErrNotFound when no conversation with that ID exists.
func (s *Store) ConversationByID(ctx context.Context, id uuid.UUID) (model.Conversation, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).conversationByID(ctx, id)
	if err != nil {
		return model.Conversation{}, fmt.Errorf("fetching conversation by id: %w", adaptererr.Decode(err))
	}

	return conversationRowToModel(row), nil
}

// ConversationBySlackThread fetches a conversation by its Slack thread
// correlation keys, resolved through the conversation_surfaces row the outbound
// Deliver wrote (provider 'slack', channel_id = channelID, thread_id = threadTS).
// Returns adaptererr.ErrNotFound when no surface (and thus no conversation) matches.
func (s *Store) ConversationBySlackThread(ctx context.Context, channelID, threadTS string) (model.Conversation, error) {
	return s.conversationByCorrelationSurface(ctx, model.SurfaceProviderSlack, channelID, threadTS, "slack thread")
}

// ConversationByTelegramMessage fetches a conversation by its Telegram
// correlation keys (chat_id + message_id of the bot's outbound message),
// resolved through the conversation_surfaces row (provider 'telegram',
// channel_id = chatID, thread_id = messageID). Returns adaptererr.ErrNotFound
// when no surface matches.
func (s *Store) ConversationByTelegramMessage(ctx context.Context, chatID, messageID string) (model.Conversation, error) {
	return s.conversationByCorrelationSurface(ctx, model.SurfaceProviderTelegram, chatID, messageID, "telegram message")
}

// conversationByCorrelationSurface resolves a conversation from the
// (provider, channel_id, thread_id) correlation surface: the surface lookup
// yields the conversation id, then the conversation is loaded. A missing surface
// surfaces as adaptererr.ErrNotFound (decoded from the :one no-rows), preserving
// the pre-surface ErrNotFound contract of the flat-column lookups.
func (s *Store) conversationByCorrelationSurface(ctx context.Context, provider, channelID, threadID, label string) (model.Conversation, error) {
	q := New(postgres.Conn(ctx, s.pool))

	surface, err := q.surfaceByChannelThread(ctx, surfaceByChannelThreadParams{
		Provider:  provider,
		ChannelID: channelID,
		ThreadID:  threadID,
	})
	if err != nil {
		return model.Conversation{}, fmt.Errorf("fetching conversation by %s: %w", label, adaptererr.Decode(err))
	}

	row, err := q.conversationByID(ctx, surface.ConversationID)
	if err != nil {
		return model.Conversation{}, fmt.Errorf("fetching conversation by %s: %w", label, adaptererr.Decode(err))
	}

	return conversationRowToModel(row), nil
}

// SetSlackThread records the Slack channel ID and thread timestamp returned by
// an outbound Deliver as the conversation's Slack correlation surface (last
// writer wins on (conversation, provider)).
func (s *Store) SetSlackThread(ctx context.Context, id uuid.UUID, channelID, threadTS string) error {
	return s.setCorrelationSurface(ctx, id, model.SurfaceProviderSlack, channelID, threadTS, "slack thread")
}

// SetTelegramThread records the Telegram chat ID and outbound message ID
// returned by an outbound Deliver as the conversation's Telegram correlation
// surface (last writer wins on (conversation, provider)).
func (s *Store) SetTelegramThread(ctx context.Context, id uuid.UUID, chatID, messageID string) error {
	return s.setCorrelationSurface(ctx, id, model.SurfaceProviderTelegram, chatID, messageID, "telegram thread")
}

// setCorrelationSurface upserts the (provider, channel_id, thread_id) surface
// that backs inbound reply correlation for a conversation's direct-ask thread.
func (s *Store) setCorrelationSurface(ctx context.Context, id uuid.UUID, provider, channelID, threadID, label string) error {
	if err := New(postgres.Conn(ctx, s.pool)).upsertCorrelationSurface(ctx, upsertCorrelationSurfaceParams{
		ID:             uuid.New(),
		ConversationID: id,
		Provider:       provider,
		ChannelID:      channelID,
		ThreadID:       threadID,
	}); err != nil {
		return fmt.Errorf("setting %s: %w", label, adaptererr.Decode(err))
	}

	return nil
}

// UpdateStatus persists a status change and bumps updated_at.
// Returns adaptererr.ErrNotFound when the conversation is absent.
func (s *Store) UpdateStatus(ctx context.Context, id uuid.UUID, status model.ConversationStatus) error {
	_, err := New(postgres.Conn(ctx, s.pool)).updateConversationStatus(ctx, updateConversationStatusParams{
		ID:     id,
		Status: string(status),
	})
	if err != nil {
		return fmt.Errorf("updating conversation status: %w", adaptererr.Decode(err))
	}

	return nil
}

// UpdateMetadata persists the agent-curated history metadata (summary, tags,
// entities) and bumps updated_at, returning the refreshed aggregate. Callers
// are expected to normalize the inputs (model.NormalizeSummary / NormalizeTags)
// before persisting. Returns adaptererr.ErrNotFound when the conversation is absent.
func (s *Store) UpdateMetadata(
	ctx context.Context,
	id uuid.UUID,
	summary string,
	tags []string,
	entities []string,
) (model.Conversation, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).updateConversationMetadata(ctx, updateConversationMetadataParams{
		ID:       id,
		Summary:  summary,
		Tags:     stringSliceOrEmpty(tags),
		Entities: stringSliceOrEmpty(entities),
	})
	if err != nil {
		return model.Conversation{}, fmt.Errorf("updating conversation metadata: %w", adaptererr.Decode(err))
	}

	return conversationRowToModel(conversationByIDRow(row)), nil
}

// DeleteConversation hard-deletes a conversation. messages,
// conversation_members, provider_messages and the attachment metadata rows fall
// via their own ON DELETE CASCADE; the attachment BYTES do not — object storage
// has no foreign key — so this enumerates the conversation's blob keys FIRST
// (before the cascade removes the attachment rows), deletes the rows, then
// removes the bytes best-effort. Without this the blobs leaked forever on every
// conversation (and cascading org/project) delete. Returns adaptererr.ErrNotFound
// when no conversation had id.
func (s *Store) DeleteConversation(ctx context.Context, id uuid.UUID) error {
	// Enumerate the blob keys before the DELETE — the attachment rows cascade
	// away with the conversation, so after the delete there is nothing left to
	// enumerate. Best-effort: a listing failure must never block the delete.
	keys := s.attachmentBlobKeys(ctx, id)

	tag, err := s.pool.Exec(ctx, `DELETE FROM conversations WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting conversation: %w", adaptererr.Decode(err))
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("conversation %s: %w", id, adaptererr.ErrNotFound)
	}

	// The rows (and their metadata) are gone; now remove the bytes. Per-blob
	// best-effort: a failed object-store delete leaves a recoverable orphan, and
	// is only logged — the conversation is already deleted and must stay deleted.
	s.deleteBlobs(ctx, keys)

	return nil
}

// attachmentBlobKeys returns the storage keys of a conversation's attachments,
// or nil when blob cleanup is disabled (no blob store wired or object storage
// off) or the lookup fails. Failures are logged, never returned: blob cleanup is
// a best-effort side of DeleteConversation, never a reason to block it.
func (s *Store) attachmentBlobKeys(ctx context.Context, id uuid.UUID) []string {
	if s.blobs == nil || !s.blobs.Enabled() {
		return nil
	}

	keys, err := New(postgres.Conn(ctx, s.pool)).storageKeysByConversation(ctx, id)
	if err != nil {
		s.logger.WarnContext(ctx, "delete conversation: listing attachment storage keys",
			slog.String("conversation_id", id.String()), slog.Any("error", err))

		return nil
	}

	return keys
}

// deleteBlobs removes each attachment blob from object storage, isolating
// failures: one failed delete is logged and the rest still run.
func (s *Store) deleteBlobs(ctx context.Context, keys []string) {
	if s.blobs == nil {
		return
	}

	for _, key := range keys {
		if err := s.blobs.Delete(ctx, key); err != nil {
			s.logger.WarnContext(ctx, "delete conversation: removing attachment blob",
				slog.String("storage_key", key), slog.Any("error", err))
		}
	}
}

// AddMessage appends a message to a conversation.
// Returns adaptererr.ErrDuplicate when a message with the same ID already exists.
func (s *Store) AddMessage(ctx context.Context, msg model.Message) error {
	// msg.Source is guaranteed valid by model.NewMessage (which defaults an
	// empty/unknown source to human), so no re-guard is needed on the write
	// side. The read side (messageToModel) still normalizes, since the DB column
	// carries no CHECK constraint and a row could be written out of band.
	_, err := New(postgres.Conn(ctx, s.pool)).addMessage(ctx, addMessageParams{
		ID:             msg.ID,
		ConversationID: msg.ConversationID,
		AuthorMemberID: pgconv.UUIDOrNull(msg.AuthorMemberID),
		Role:           msg.Role.String(),
		Body:           msg.Body,
		Source:         string(msg.Source),
		AgentClient:    msg.AgentClient,
	})
	if err != nil {
		return fmt.Errorf("adding message: %w", adaptererr.Decode(err))
	}

	return nil
}

// MessagesByConversation returns all messages for conversationID in
// chronological (created_at ASC) order.
func (s *Store) MessagesByConversation(ctx context.Context, conversationID uuid.UUID) ([]model.Message, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).messagesByConversation(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("listing messages: %w", adaptererr.Decode(err))
	}

	messages := make([]model.Message, len(rows))
	for i, r := range rows {
		messages[i] = messageToModel(r)
	}

	return messages, nil
}

// conversationToRecord maps a conversation aggregate to its query read model,
// resolving the display title. Shared by every read path that lists or fetches
// conversations.
func conversationToRecord(conv model.Conversation) query.ConversationRecord {
	return query.ConversationRecord{
		ID:                conv.ID,
		ProjectID:         conv.ProjectID,
		AskerMemberID:     conv.AskerMemberID,
		ResponderMemberID: conv.ResponderMemberID,
		Status:            conv.Status,
		Question:          conv.Question,
		Title:             conv.DisplayTitle(),
		CreatedAt:         conv.CreatedAt,
		UpdatedAt:         conv.UpdatedAt,
	}
}

// conversationsToRecords maps a slice of conversation aggregates to read models.
func conversationsToRecords(convs []model.Conversation) []query.ConversationRecord {
	records := make([]query.ConversationRecord, len(convs))
	for i, c := range convs {
		records[i] = conversationToRecord(c)
	}

	return records
}

// ReadConversation returns the core conversation read model (display title
// resolved), so the read side never touches the domain aggregate.
func (s *Store) ReadConversation(ctx context.Context, id uuid.UUID) (query.ConversationRecord, error) {
	conv, err := s.ConversationByID(ctx, id)
	if err != nil {
		return query.ConversationRecord{}, err
	}

	return conversationToRecord(conv), nil
}

// ReadMessages returns a conversation's messages as views (without attachments,
// which the read handler resolves and signs separately).
func (s *Store) ReadMessages(ctx context.Context, conversationID uuid.UUID) ([]query.MessageView, error) {
	msgs, err := s.MessagesByConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}

	views := make([]query.MessageView, len(msgs))
	for i, m := range msgs {
		views[i] = query.MessageView{
			ID:             m.ID,
			AuthorMemberID: m.AuthorMemberID,
			Role:           m.Role,
			Body:           m.Body,
			Source:         m.Source,
			AgentClient:    m.AgentClient,
			CreatedAt:      m.CreatedAt,
		}
	}

	return views, nil
}

// AddParticipant adds member to the conversation's explicit participant set
// (idempotent via ON CONFLICT DO NOTHING). addedBy may be uuid.Nil.
func (s *Store) AddParticipant(ctx context.Context, conversationID, memberID, addedBy uuid.UUID) error {
	if err := New(postgres.Conn(ctx, s.pool)).addParticipant(ctx, addParticipantParams{
		ConversationID: conversationID,
		MemberID:       memberID,
		AddedBy:        pgconv.UUIDOrNull(addedBy),
	}); err != nil {
		return fmt.Errorf("adding participant: %w", adaptererr.Decode(err))
	}

	return nil
}

// ParticipantsByConversations batch-resolves the explicitly-added participants
// for a set of conversations (one query), keyed by conversation id.
func (s *Store) ParticipantsByConversations(ctx context.Context, conversationIDs []uuid.UUID) (map[uuid.UUID][]model.ConversationParticipant, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).participantsByConversations(ctx, conversationIDs)
	if err != nil {
		return nil, fmt.Errorf("listing participants batch: %w", adaptererr.Decode(err))
	}

	out := make(map[uuid.UUID][]model.ConversationParticipant, len(conversationIDs))
	for _, r := range rows {
		out[r.ConversationID] = append(out[r.ConversationID], model.ConversationParticipant{
			MemberID: r.MemberID,
			AddedBy:  pgconv.UUIDFromPgtype(r.AddedBy),
			AddedAt:  r.AddedAt.Time,
		})
	}

	return out, nil
}

// ActiveCandidatesByConversations batch-resolves the still-active pool
// candidates (contacted, not yet excluded) for a set of conversations, keyed by
// conversation id.
func (s *Store) ActiveCandidatesByConversations(ctx context.Context, conversationIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).activeCandidatesByConversations(ctx, conversationIDs)
	if err != nil {
		return nil, fmt.Errorf("listing candidates batch: %w", adaptererr.Decode(err))
	}

	out := make(map[uuid.UUID][]uuid.UUID, len(conversationIDs))
	for _, r := range rows {
		out[r.ConversationID] = append(out[r.ConversationID], r.MemberID)
	}

	return out, nil
}

// MemberNamesByIDs batch-resolves display names for a set of members (one
// query), keyed by member id.
func (s *Store) MemberNamesByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).memberNamesByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("resolving member names: %w", adaptererr.Decode(err))
	}

	out := make(map[uuid.UUID]string, len(rows))
	for _, r := range rows {
		out[r.ID] = pgconv.StringFromText(r.DisplayName)
	}

	return out, nil
}

// ParticipantsByConversation returns the explicitly-added participants (the
// asker and assigned responder are implicit and not stored here).
func (s *Store) ParticipantsByConversation(ctx context.Context, conversationID uuid.UUID) ([]model.ConversationParticipant, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).participantsByConversation(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("listing participants: %w", adaptererr.Decode(err))
	}

	out := make([]model.ConversationParticipant, len(rows))
	for i, r := range rows {
		out[i] = model.ConversationParticipant{
			MemberID: r.MemberID,
			AddedBy:  pgconv.UUIDFromPgtype(r.AddedBy),
			AddedAt:  r.AddedAt.Time,
		}
	}

	return out, nil
}

// stringSliceOrEmpty coerces a nil slice to a non-nil empty slice, so a NOT NULL
// TEXT[] column is written as '{}' rather than NULL on the create/update paths.
func stringSliceOrEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}

// conversationRowToModel maps a conversationByIDRow to a domain Conversation.
// All other per-query conversation row types are structurally identical to
// conversationByIDRow and may be cast to it before calling this function.
func conversationRowToModel(r conversationByIDRow) model.Conversation {
	return model.Conversation{
		ID:                r.ID,
		ProjectID:         r.ProjectID,
		AskerMemberID:     r.AskerMemberID,
		ResponderMemberID: pgconv.UUIDFromPgtype(r.ResponderMemberID),
		Status:            model.ConversationStatus(r.Status),
		Question:          r.Question,
		Title:             r.Title,
		Context:           r.Context,
		Summary:           r.Summary,
		Tags:              r.Tags,
		Entities:          r.Entities,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
}

// messageToModel maps a sqlc Message row to a domain Message.
func messageToModel(r Message) model.Message {
	source := model.MessageSource(r.Source)
	if !source.Valid() {
		source = model.MessageSourceHuman
	}

	return model.Message{
		ID:             r.ID,
		ConversationID: r.ConversationID,
		AuthorMemberID: pgconv.UUIDFromPgtype(r.AuthorMemberID),
		Role:           messageRoleFromString(r.Role),
		Body:           r.Body,
		Source:         source,
		AgentClient:    r.AgentClient,
		CreatedAt:      r.CreatedAt,
	}
}

// ConversationsByProjectIDs returns conversations across a multi-project
// scope, newest first, each paired with its project's resolved name. An empty
// projectIDs falls back to every non-archived project in orgID (the org-wide
// read, gated to an org-admin caller by the transport); a non-empty slice
// restricts to exactly those projects. Archived projects are always excluded.
// status is optional (empty = all). includeAll (the org-admin path) returns
// every conversation in scope; otherwise results are scoped to those
// callerMemberID asked or is the assigned responder for. Hand-written pgx
// (not sqlc) because the optional status filter and uuid[] scope do not fit
// sqlc's named-query model.
func (s *Store) ConversationsByProjectIDs(
	ctx context.Context,
	orgID uuid.UUID,
	projectIDs []uuid.UUID,
	status string,
	callerMemberID uuid.UUID,
	includeAll bool,
) ([]query.ConversationWithProject, error) {
	ids := projectIDs
	if ids == nil {
		ids = []uuid.UUID{}
	}

	var (
		rows pgx.Rows
		err  error
	)

	if includeAll {
		rows, err = s.pool.Query(ctx, conversationsByScopeSQL, orgID, ids, status)
	} else {
		rows, err = s.pool.Query(ctx, conversationsByScopeForMemberSQL, orgID, ids, status, callerMemberID)
	}

	if err != nil {
		return nil, fmt.Errorf("listing conversations by scope: %w", adaptererr.Decode(err))
	}

	return scanConversationsWithProject(rows, "conversations by scope")
}

// InboxByMember returns the open conversations addressed to targetID as the
// responder, newest first. This is the read model behind the dashboard inbox.
func (s *Store) InboxByMember(ctx context.Context, targetID uuid.UUID) ([]query.ConversationRecord, error) {
	rows, err := s.pool.Query(ctx, inboxByMemberSQL, targetID)
	if err != nil {
		return nil, fmt.Errorf("listing inbox by member: %w", adaptererr.Decode(err))
	}

	convs, err := scanConversations(rows, "inbox")
	if err != nil {
		return nil, err
	}

	return conversationsToRecords(convs), nil
}

// OpenConversationsByOrg returns every open conversation across all projects
// owned by orgID, newest first. It is the org-admin inbox bypass.
func (s *Store) OpenConversationsByOrg(ctx context.Context, orgID uuid.UUID) ([]query.ConversationRecord, error) {
	rows, err := s.pool.Query(ctx, openConversationsByOrgSQL, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing open conversations by org: %w", adaptererr.Decode(err))
	}

	convs, err := scanConversations(rows, "open conversations by org")
	if err != nil {
		return nil, err
	}

	return conversationsToRecords(convs), nil
}

// HistoryVocabulary returns the distinct tags and entities curated across the
// conversations of projectIDs, most-frequent first, each list capped at limit.
// Archived projects are excluded; an empty projectIDs short-circuits to an
// empty vocabulary (no query). This powers the vocabulary reinjection that
// keeps the agent reusing established labels instead of inventing synonyms.
func (s *Store) HistoryVocabulary(ctx context.Context, projectIDs []uuid.UUID, limit int) (query.Vocabulary, error) {
	if len(projectIDs) == 0 || limit <= 0 {
		return query.Vocabulary{Tags: nil, Entities: nil}, nil
	}

	tags, err := s.scanLabels(ctx, distinctTagsByScopeSQL, projectIDs, limit, "history tags")
	if err != nil {
		return query.Vocabulary{}, err
	}

	entities, err := s.scanLabels(ctx, distinctEntitiesByScopeSQL, projectIDs, limit, "history entities")
	if err != nil {
		return query.Vocabulary{}, err
	}

	return query.Vocabulary{Tags: tags, Entities: entities}, nil
}

// scanLabels runs a single-column label query (unnest + group-by) over the
// project scope and drains it into a string slice, closing rows on return.
func (s *Store) scanLabels(ctx context.Context, sql string, projectIDs []uuid.UUID, limit int, label string) ([]string, error) {
	rows, err := s.pool.Query(ctx, sql, projectIDs, limit)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", label, adaptererr.Decode(err))
	}

	defer rows.Close()

	var out []string

	for rows.Next() {
		var value string

		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("scanning %s row: %w", label, err)
		}

		out = append(out, value)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating %s: %w", label, err)
	}

	return out, nil
}

// SearchHistory runs the deterministic (embedding-free) history search across
// projectIDs — see searchHistorySQL for the scope, match arms, and ranking.
// ALL conversation statuses are eligible. Returns an empty (non-nil) slice when
// nothing matches.
func (s *Store) SearchHistory(
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

	rows, err := s.pool.Query(ctx, searchHistorySQL,
		orgID, ids, queryText, stringSliceOrEmpty(tags), historyTrgmThreshold, topK,
	)
	if err != nil {
		return nil, fmt.Errorf("searching history: %w", adaptererr.Decode(err))
	}

	return scanHistoryHits(rows, "history search")
}

// RecentHistory browses the most-recent conversations in scope with no match
// required — see recentHistorySQL. An empty status browses every status.
// Returns an empty (non-nil) slice when nothing is in scope.
func (s *Store) RecentHistory(
	ctx context.Context,
	orgID uuid.UUID,
	projectIDs []uuid.UUID,
	status string,
	topK int,
) ([]query.HistoryHit, error) {
	ids := projectIDs
	if ids == nil {
		ids = []uuid.UUID{}
	}

	rows, err := s.pool.Query(ctx, recentHistorySQL, orgID, ids, status, topK)
	if err != nil {
		return nil, fmt.Errorf("browsing history: %w", adaptererr.Decode(err))
	}

	return scanHistoryHits(rows, "recent history")
}

// HistoryStatusCounts returns the per-status conversation tally for the scope —
// see historyStatusCountsSQL. Statuses with no conversations are absent from the
// grouped result and left at zero.
func (s *Store) HistoryStatusCounts(
	ctx context.Context,
	orgID uuid.UUID,
	projectIDs []uuid.UUID,
) (query.HistoryStatusCounts, error) {
	ids := projectIDs
	if ids == nil {
		ids = []uuid.UUID{}
	}

	rows, err := s.pool.Query(ctx, historyStatusCountsSQL, orgID, ids)
	if err != nil {
		return query.HistoryStatusCounts{}, fmt.Errorf("counting history statuses: %w", adaptererr.Decode(err))
	}
	defer rows.Close()

	var counts query.HistoryStatusCounts

	for rows.Next() {
		var (
			status string
			n      int
		)

		if err := rows.Scan(&status, &n); err != nil {
			return query.HistoryStatusCounts{}, fmt.Errorf("scanning history status count row: %w", err)
		}

		counts.All += n

		switch model.ConversationStatus(status) {
		case model.ConversationStatusOpen:
			counts.Open = n
		case model.ConversationStatusAnswered:
			counts.Answered = n
		case model.ConversationStatusResolved:
			counts.Resolved = n
		case model.ConversationStatusTimedOut:
			counts.TimedOut = n
		case model.ConversationStatusDismissed:
			counts.Dismissed = n
		}
	}

	if err := rows.Err(); err != nil {
		return query.HistoryStatusCounts{}, fmt.Errorf("iterating history status counts: %w", adaptererr.Decode(err))
	}

	return counts, nil
}

// scanHistoryHits drains rows selecting historyHitColumns followed by a trailing
// real score into query.HistoryHit values, resolving the display title (empty
// title falls back to the truncated question via the domain helper). Returns an
// empty (non-nil) slice when none match. Closes rows on return.
func scanHistoryHits(rows pgx.Rows, label string) ([]query.HistoryHit, error) {
	defer rows.Close()

	var results []query.HistoryHit

	for rows.Next() {
		var (
			hit      query.HistoryHit
			question string
			status   string
			score    float32
		)

		if err := rows.Scan(
			&hit.ConversationID,
			&hit.ProjectID,
			&hit.ProjectName,
			&hit.Title,
			&question,
			&hit.Summary,
			&status,
			&hit.Tags,
			&hit.Entities,
			&hit.AskerMemberID,
			&hit.CreatedAt,
			&hit.UpdatedAt,
			&score,
		); err != nil {
			return nil, fmt.Errorf("scanning %s row: %w", label, err)
		}

		hit.Status = model.ConversationStatus(status)
		hit.Score = score
		// Reuse the domain's title-fallback so the read side never generates text,
		// only trims it — identical to the list/get read paths.
		hit.Title = model.DisplayTitle(hit.Title, question)

		results = append(results, hit)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating %s: %w", label, adaptererr.Decode(err))
	}

	if results == nil {
		return []query.HistoryHit{}, nil
	}

	return results, nil
}

// scanConversations drains rows (which must select the full conversation column
// list in the canonical order) into domain Conversations, closing rows on return.
func scanConversations(rows pgx.Rows, label string) ([]model.Conversation, error) {
	defer rows.Close()

	var results []model.Conversation

	for rows.Next() {
		var c conversationByIDRow

		if err := rows.Scan(
			&c.ID,
			&c.ProjectID,
			&c.AskerMemberID,
			&c.ResponderMemberID,
			&c.Status,
			&c.Question,
			&c.Title,
			&c.Context,
			&c.CreatedAt,
			&c.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning %s row: %w", label, err)
		}

		results = append(results, conversationRowToModel(c))
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating %s: %w", label, err)
	}

	return results, nil
}

// scanConversationsWithProject drains rows (which must select the full
// conversation column list in the canonical order, followed by the project's
// name) into query.ConversationWithProject values, closing rows on
// return.
func scanConversationsWithProject(rows pgx.Rows, label string) ([]query.ConversationWithProject, error) {
	defer rows.Close()

	var results []query.ConversationWithProject

	for rows.Next() {
		var (
			c           conversationByIDRow
			projectName string
		)

		if err := rows.Scan(
			&c.ID,
			&c.ProjectID,
			&c.AskerMemberID,
			&c.ResponderMemberID,
			&c.Status,
			&c.Question,
			&c.Title,
			&c.Context,
			&c.CreatedAt,
			&c.UpdatedAt,
			&projectName,
		); err != nil {
			return nil, fmt.Errorf("scanning %s row: %w", label, err)
		}

		results = append(results, query.ConversationWithProject{
			ConversationRecord: conversationToRecord(conversationRowToModel(c)),
			ProjectName:        projectName,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating %s: %w", label, err)
	}

	return results, nil
}

// messageRoleFromString parses the DB text column back into a domain MessageRole.
func messageRoleFromString(s string) model.MessageRole {
	switch s {
	case "question":
		return model.MessageRoleQuestion
	case "answer":
		return model.MessageRoleAnswer
	case "follow_up":
		return model.MessageRoleFollowUp
	case "system":
		return model.MessageRoleSystem
	case "second_opinion":
		return model.MessageRoleSecondOpinion
	default:
		return model.MessageRoleUnspecified
	}
}
