// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"

	"github.com/google/uuid"
)

// ListInboxQuery is the input for listing a responder's pending questions.
type ListInboxQuery struct {
	// ResponderMemberID is the caller (resolved from the auth token).
	ResponderMemberID uuid.UUID
	// OrgID is the caller's organization; used only for the org-admin bypass.
	OrgID uuid.UUID `exhaustruct:"optional"`
	// IsOrgAdmin lifts the responder scope: an org admin sees every open
	// conversation across the org, not only those addressed to them.
	IsOrgAdmin bool `exhaustruct:"optional"`
}

// openPoolReader lists the unanswered pool conversations a member was
// contacted on. *conversation.CandidateStore satisfies it.
type openPoolReader interface {
	OpenPoolFor(ctx context.Context, memberID uuid.UUID) ([]ConversationRecord, error)
}

// ListInboxHandler handles ListInboxQuery.
type ListInboxHandler struct {
	reader InboxReader
	pool   openPoolReader
}

// MustNewListInboxHandler builds a handler. It panics on nil
// dependencies.
func MustNewListInboxHandler(reader InboxReader, pool openPoolReader) ListInboxHandler {
	if reader == nil {
		panic("ListInboxHandler requires a non-nil InboxReader")
	}

	if pool == nil {
		panic("ListInboxHandler requires a non-nil openPoolReader")
	}

	return ListInboxHandler{reader: reader, pool: pool}
}

// Handle returns the unanswered pool conversations the caller was contacted
// on first (awaiting anyone's reply), then the caller's own open
// conversations (as responder), each newest first. An org admin instead
// receives every open conversation across their org (the admin bypass). An
// empty slice is returned (not nil) when the inbox is empty.
func (h ListInboxHandler) Handle(ctx context.Context, q ListInboxQuery) ([]InboxItem, error) {
	pool, err := h.pool.OpenPoolFor(ctx, q.ResponderMemberID)
	if err != nil {
		return nil, err
	}

	convs, err := h.fetch(ctx, q)
	if err != nil {
		return nil, err
	}

	items := make([]InboxItem, 0, len(pool)+len(convs))

	for _, c := range pool {
		items = append(items, inboxItemFrom(c))
	}

	for _, c := range convs {
		// The org-admin bypass may also surface unanswered pool conversations;
		// don't list them twice.
		if c.ResponderMemberID == uuid.Nil {
			continue
		}

		items = append(items, inboxItemFrom(c))
	}

	return items, nil
}

// inboxItemFrom maps a conversation to its inbox DTO.
func inboxItemFrom(c ConversationRecord) InboxItem {
	return InboxItem{
		ConversationID: c.ID,
		ProjectID:      c.ProjectID,
		Question:       c.Question,
		Title:          c.Title,
		AskerMemberID:  c.AskerMemberID,
		Status:         c.Status,
		CreatedAt:      c.CreatedAt,
	}
}

// fetch selects the inbox source: the org-wide open set for an org admin (with a
// resolved org), otherwise the caller's responder-scoped open conversations.
func (h ListInboxHandler) fetch(ctx context.Context, q ListInboxQuery) ([]ConversationRecord, error) {
	if q.IsOrgAdmin && q.OrgID != uuid.Nil {
		return h.reader.OpenConversationsByOrg(ctx, q.OrgID)
	}

	return h.reader.InboxByMember(ctx, q.ResponderMemberID)
}
