// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// KnowledgeEntryView is the read-side DTO for one curated knowledge entry in the
// dashboard Knowledge section. It carries every field the CRUD UI renders — the
// full answer body (unlike a search hit, which only needs the snippet), the
// resolved project name for org-wide vs project-scoped display, and the
// provenance/status labels.
type KnowledgeEntryView struct {
	ID    uuid.UUID
	OrgID uuid.UUID
	// ProjectID is uuid.Nil for an org-wide entry.
	ProjectID uuid.UUID `exhaustruct:"optional"`
	// ProjectName is the resolved name of ProjectID; empty for an org-wide entry.
	ProjectName    string `exhaustruct:"optional"`
	Question       string
	Answer         string
	Summary        string    `exhaustruct:"optional"`
	Tags           []string  `exhaustruct:"optional"`
	Entities       []string  `exhaustruct:"optional"`
	AuthorMemberID uuid.UUID `exhaustruct:"optional"`
	// Source is the provenance label ("curated" | "agent_suggested").
	Source string
	// Status is the lifecycle label ("active" | "stale" | "pending").
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// KnowledgeEntryReader is the read-side port for listing curated entries.
// Satisfied by *knowledge.Store.
type KnowledgeEntryReader interface {
	// ListEntries returns curated entries in scope (org + org-wide + optional
	// project set), every status, newest first, as read-side views.
	ListEntries(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID) ([]KnowledgeEntryView, error)
}

// ListKnowledgeEntriesQuery lists curated entries for the dashboard Knowledge
// section. Scope mirrors SearchHistory (org + optional project set); an empty
// project set is the org-wide read.
type ListKnowledgeEntriesQuery struct {
	// OrgID is the caller's organization (from the RBAC context, never the
	// request).
	OrgID uuid.UUID
	// ProjectIDs narrows to a chosen subset of the org's projects; empty = every
	// project the caller may see (org-wide read). Org-wide entries (no project)
	// are always included.
	ProjectIDs []uuid.UUID `exhaustruct:"optional"`
}

// ListKnowledgeEntriesHandler handles ListKnowledgeEntriesQuery.
type ListKnowledgeEntriesHandler struct {
	reader KnowledgeEntryReader
}

// MustNewListKnowledgeEntriesHandler builds a handler. It panics on a
// nil dependency.
func MustNewListKnowledgeEntriesHandler(reader KnowledgeEntryReader) ListKnowledgeEntriesHandler {
	if reader == nil {
		panic("ListKnowledgeEntriesHandler requires a non-nil KnowledgeEntryReader")
	}

	return ListKnowledgeEntriesHandler{reader: reader}
}

// Handle returns the curated entries in scope, newest first.
func (h ListKnowledgeEntriesHandler) Handle(ctx context.Context, q ListKnowledgeEntriesQuery) ([]KnowledgeEntryView, error) {
	views, err := h.reader.ListEntries(ctx, q.OrgID, q.ProjectIDs)
	if err != nil {
		return nil, errs.InternalError{Err: fmt.Errorf("listing knowledge entries: %w", err)}
	}

	return views, nil
}

// PendingKnowledgeReader is the read-side port for the agent-suggested approval
// queue. Satisfied by *knowledge.Store.
type PendingKnowledgeReader interface {
	// ListPending returns the agent-suggested entries awaiting review
	// (source='agent_suggested' AND status='pending') in scope, newest first, as
	// read-side views.
	ListPending(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID) ([]KnowledgeEntryView, error)
}

// ListPendingKnowledgeEntriesQuery lists the agent-suggested entries awaiting a
// human's approve/dismiss decision (the trust-gradient review queue). Scope
// mirrors ListKnowledgeEntriesQuery.
type ListPendingKnowledgeEntriesQuery struct {
	// OrgID is the caller's organization (from the RBAC context, never the
	// request).
	OrgID uuid.UUID
	// ProjectIDs narrows to a chosen subset of the org's projects; empty = every
	// project the caller may see (org-wide read). Org-wide entries (no project)
	// are always included.
	ProjectIDs []uuid.UUID `exhaustruct:"optional"`
}

// ListPendingKnowledgeEntriesHandler handles ListPendingKnowledgeEntriesQuery.
type ListPendingKnowledgeEntriesHandler struct {
	reader PendingKnowledgeReader
}

// MustNewListPendingKnowledgeEntriesHandler builds a handler. It panics on a nil dependency.
func MustNewListPendingKnowledgeEntriesHandler(reader PendingKnowledgeReader) ListPendingKnowledgeEntriesHandler {
	if reader == nil {
		panic("ListPendingKnowledgeEntriesHandler requires a non-nil PendingKnowledgeReader")
	}

	return ListPendingKnowledgeEntriesHandler{reader: reader}
}

// Handle returns the pending agent-suggested entries in scope, newest first.
func (h ListPendingKnowledgeEntriesHandler) Handle(ctx context.Context, q ListPendingKnowledgeEntriesQuery) ([]KnowledgeEntryView, error) {
	views, err := h.reader.ListPending(ctx, q.OrgID, q.ProjectIDs)
	if err != nil {
		return nil, errs.InternalError{Err: fmt.Errorf("listing pending knowledge entries: %w", err)}
	}

	return views, nil
}
