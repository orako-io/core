// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// KnowledgeSource records how a curated knowledge entry came to exist. It is the
// trust gradient's provenance label — never something the searching agent must
// branch on (search returns curated and organic hits under one shape).
type KnowledgeSource string

// Recognized knowledge-entry sources. Mirror the source column CHECK on
// knowledge_entries.
const (
	// KnowledgeSourceCurated is an entry a human authored/maintained (FAQ, config
	// fix, runbook) — the Phase 1 path (dashboard + add_knowledge).
	KnowledgeSourceCurated KnowledgeSource = "curated"
	// KnowledgeSourceAgentSuggested is an entry an agent proposed from its own
	// work (Phase 2, approval-queued); reserved here so the column accepts it.
	KnowledgeSourceAgentSuggested KnowledgeSource = "agent_suggested"
)

// Valid reports whether s is a recognized source.
func (s KnowledgeSource) Valid() bool {
	switch s {
	case KnowledgeSourceCurated, KnowledgeSourceAgentSuggested:
		return true
	}

	return false
}

// KnowledgeStatus is the lifecycle state of a curated knowledge entry. Only
// active entries are returned by search_knowledge.
type KnowledgeStatus string

// Recognized knowledge-entry statuses. Mirror the status column CHECK on
// knowledge_entries.
const (
	// KnowledgeStatusActive is a live entry, eligible for search_knowledge.
	KnowledgeStatusActive KnowledgeStatus = "active"
	// KnowledgeStatusStale flags an entry a human marked out-of-date; it drops
	// out of search until re-activated.
	KnowledgeStatusStale KnowledgeStatus = "stale"
	// KnowledgeStatusPending is an agent-suggested entry awaiting approval
	// (Phase 2); reserved here so the column accepts it.
	KnowledgeStatusPending KnowledgeStatus = "pending"
)

// Valid reports whether s is a recognized status.
func (s KnowledgeStatus) Valid() bool {
	switch s {
	case KnowledgeStatusActive, KnowledgeStatusStale, KnowledgeStatusPending:
		return true
	}

	return false
}

// KnowledgeEntry is a curated, searchable answer authored/maintained by a team
// (decision 2026-07-26). Its searchable/returnable shape mirrors a resolved
// conversation 1:1 (question, answer, summary, tags, entities) so it is
// indistinguishable from organic history to a searching agent; only its
// attribution ("Curated by {org}") and analytics treatment differ.
type KnowledgeEntry struct {
	// ID uniquely identifies the entry.
	ID uuid.UUID
	// OrgID scopes the entry to an organization. Always set from the caller
	// identity, never a request payload.
	OrgID uuid.UUID
	// ProjectID scopes the entry to a project; uuid.Nil = org-wide (visible in
	// every project of the org).
	ProjectID uuid.UUID `exhaustruct:"optional"`
	// Question is what the entry answers (~ a conversation's question/title).
	Question string
	// Answer is the curated answer body.
	Answer string
	// Summary is the one-line gist, normalized exactly like conversations.summary.
	Summary string `exhaustruct:"optional"`
	// Tags are normalized facet labels (lowercase, trimmed, deduped).
	Tags []string `exhaustruct:"optional"`
	// Entities are normalized named things the entry is about.
	Entities []string `exhaustruct:"optional"`
	// AuthorMemberID is who curated it (attribution/label only); uuid.Nil when
	// unattributed or the author was later removed.
	AuthorMemberID uuid.UUID `exhaustruct:"optional"`
	// Source is the provenance (curated / agent_suggested).
	Source KnowledgeSource
	// Status is the lifecycle state (active / stale / pending).
	Status KnowledgeStatus
	// CreatedAt is when the entry was authored.
	CreatedAt time.Time `exhaustruct:"optional"`
	// UpdatedAt is when the entry was last modified.
	UpdatedAt time.Time `exhaustruct:"optional"`
}

// Over-length validation reason for a curated answer (kept in sync with
// MaxAnswerRunes).
const reasonKnowledgeAnswerTooLong = "answer is too long (max 10000 characters)"

// fieldAnswer is the validation field name for a curated answer.
const fieldAnswer = "answer"

// NewKnowledgeEntry builds a curated KnowledgeEntry (source=curated,
// status=active), validating every invariant and normalizing the metadata
// fields with the SAME normalizers used for conversation history
// (NormalizeSummary/NormalizeTags/NormalizeEntities), so a curated entry indexes
// and searches identically to a resolved conversation. This is the human-authored
// publish path (dashboard + add_knowledge + promote-conversation).
func NewKnowledgeEntry(
	id uuid.UUID,
	orgID uuid.UUID,
	projectID uuid.UUID,
	authorMemberID uuid.UUID,
	question string,
	answer string,
	summary string,
	tags []string,
	entities []string,
) (KnowledgeEntry, error) {
	return newKnowledgeEntry(id, orgID, projectID, authorMemberID, question, answer, summary, tags, entities,
		KnowledgeSourceCurated, KnowledgeStatusActive)
}

// NewSuggestedKnowledgeEntry builds an agent-suggested KnowledgeEntry
// (source=agent_suggested, status=pending): the trust gradient's proposal path
// (the suggest_knowledge MCP tool). It is validated + normalized exactly like a
// curated entry, but lands PENDING — never searchable until a human approves it,
// so an over-eager agent cannot pollute the base.
func NewSuggestedKnowledgeEntry(
	id uuid.UUID,
	orgID uuid.UUID,
	projectID uuid.UUID,
	authorMemberID uuid.UUID,
	question string,
	answer string,
	summary string,
	tags []string,
	entities []string,
) (KnowledgeEntry, error) {
	return newKnowledgeEntry(id, orgID, projectID, authorMemberID, question, answer, summary, tags, entities,
		KnowledgeSourceAgentSuggested, KnowledgeStatusPending)
}

// newKnowledgeEntry is the shared builder behind the curated and agent-suggested
// constructors: it validates every invariant and normalizes the metadata fields,
// stamping the caller-chosen source + status.
func newKnowledgeEntry(
	id uuid.UUID,
	orgID uuid.UUID,
	projectID uuid.UUID,
	authorMemberID uuid.UUID,
	question string,
	answer string,
	summary string,
	tags []string,
	entities []string,
	source KnowledgeSource,
	status KnowledgeStatus,
) (KnowledgeEntry, error) {
	if id == uuid.Nil {
		return KnowledgeEntry{}, errs.InvalidError{Field: "id", Reason: nilUUIDReason}
	}

	if orgID == uuid.Nil {
		return KnowledgeEntry{}, errs.InvalidError{Field: "org_id", Reason: nilUUIDReason}
	}

	q := strings.TrimSpace(question)
	if q == "" {
		return KnowledgeEntry{}, errs.InvalidError{Field: fieldQuestion, Reason: emptyReason}
	}

	if exceedsRunes(q, MaxQuestionRunes) {
		return KnowledgeEntry{}, errs.InvalidError{Field: fieldQuestion, Reason: reasonQuestionTooLong}
	}

	a := strings.TrimSpace(answer)
	if a == "" {
		return KnowledgeEntry{}, errs.InvalidError{Field: fieldAnswer, Reason: emptyReason}
	}

	if exceedsRunes(a, MaxAnswerRunes) {
		return KnowledgeEntry{}, errs.InvalidError{Field: fieldAnswer, Reason: reasonKnowledgeAnswerTooLong}
	}

	now := time.Now().UTC()

	return KnowledgeEntry{
		ID:             id,
		OrgID:          orgID,
		ProjectID:      projectID,
		Question:       q,
		Answer:         a,
		Summary:        NormalizeSummary(summary),
		Tags:           NormalizeTags(tags),
		Entities:       NormalizeTags(entities),
		AuthorMemberID: authorMemberID,
		Source:         source,
		Status:         status,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}
