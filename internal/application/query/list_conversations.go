// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"

	"github.com/google/uuid"
)

// ListConversationsQuery is the input for listing conversations across a
// multi-project scope.
type ListConversationsQuery struct {
	// ProjectIDs scopes the result. Empty falls back to every non-archived
	// project in OrgID the caller may see (resolved by the transport before
	// this query runs — see Server.resolveProjectScope); a non-empty slice
	// restricts to exactly those projects. Archived projects are always
	// excluded regardless of this set.
	ProjectIDs []uuid.UUID `exhaustruct:"optional"`
	// OrgID is the caller's organization; used as the org-wide fallback scope
	// when ProjectIDs is empty. uuid.Nil disables the org filter (self-host /
	// unresolved org), mirroring ListProjectsQuery.OrgID.
	OrgID uuid.UUID `exhaustruct:"optional"`
	// Status is an optional filter ("open", "answered", "resolved", "timed_out").
	// An empty string returns all statuses.
	Status string
	// CallerMemberID is the authenticated caller. A non-admin caller sees only
	// conversations they asked or are the assigned responder for.
	CallerMemberID uuid.UUID `exhaustruct:"optional"`
	// IsOrgAdmin lifts the owner-or-responder filter: an org admin sees every
	// conversation in scope.
	IsOrgAdmin bool `exhaustruct:"optional"`
}

// ListConversationsHandler handles ListConversationsQuery.
type ListConversationsHandler struct {
	reader       ConversationsByProjectReader
	participants participantsBatchReader
	names        memberNamesReader
}

// MustNewListConversationsHandler builds a handler. It panics on nil
// dependencies.
func MustNewListConversationsHandler(reader ConversationsByProjectReader, participants participantsBatchReader, names memberNamesReader) ListConversationsHandler {
	if reader == nil || participants == nil || names == nil {
		panic("ListConversationsHandler requires non-nil reader, participants, and names")
	}

	return ListConversationsHandler{reader: reader, participants: participants, names: names}
}

// Handle returns conversations in scope, newest first, as ConversationSummary
// DTOs — each carrying its project's id and resolved name. An empty slice is
// returned (not nil) when there are no conversations.
func (h ListConversationsHandler) Handle(ctx context.Context, q ListConversationsQuery) ([]ConversationSummary, error) {
	convs, err := h.reader.ConversationsByProjectIDs(ctx, q.OrgID, q.ProjectIDs, q.Status, q.CallerMemberID, q.IsOrgAdmin)
	if err != nil {
		return nil, translateReadError(err, "conversations")
	}

	sources := make([]participantSource, len(convs))
	for i, cwp := range convs {
		sources[i] = participantSource{
			ConversationID:    cwp.ID,
			AskerMemberID:     cwp.AskerMemberID,
			ResponderMemberID: cwp.ResponderMemberID,
		}
	}

	participants, err := resolveParticipants(ctx, h.participants, h.names, sources)
	if err != nil {
		return nil, translateReadError(err, "conversation_participants")
	}

	summaries := make([]ConversationSummary, len(convs))

	for i, cwp := range convs {
		c := cwp.ConversationRecord
		summaries[i] = ConversationSummary{
			ID:                c.ID,
			Status:            c.Status,
			Question:          c.Question,
			Title:             c.Title,
			ResponderMemberID: c.ResponderMemberID,
			CreatedAt:         c.CreatedAt,
			ProjectID:         c.ProjectID,
			ProjectName:       cwp.ProjectName,
			Participants:      participants[c.ID],
		}
	}

	return summaries, nil
}
