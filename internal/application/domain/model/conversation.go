// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// ConversationStatus is the lifecycle state of a conversation.
type ConversationStatus string

// Recognized conversation status values. Mirror the status column in the
// conversations table.
const (
	ConversationStatusOpen     ConversationStatus = "open"
	ConversationStatusAnswered ConversationStatus = "answered"
	ConversationStatusResolved ConversationStatus = "resolved"
	ConversationStatusTimedOut ConversationStatus = "timed_out"
	// ConversationStatusDismissed is a terminal state for a conversation closed
	// WITHOUT an answer: the asker (or a responder) gave up on it in the
	// dashboard. Unlike Resolved, it records no answer — there is nothing to
	// resolve — so it stays distinct from a resolved conversation in metrics.
	ConversationStatusDismissed ConversationStatus = "dismissed"
)

// Conversation is a durable async thread between an agent (asker) and a
// responder (answerer). Its native-platform correlation (the Slack thread or
// Telegram reply anchor a responder answers in) lives on conversation_surfaces,
// not on the aggregate.
type Conversation struct {
	// ID uniquely identifies the conversation.
	ID uuid.UUID
	// ProjectID scopes the conversation to a project.
	ProjectID uuid.UUID
	// AskerMemberID is the agent-side member who opened the conversation.
	AskerMemberID uuid.UUID
	// ResponderMemberID is the targeted answerer; zero when not yet assigned.
	ResponderMemberID uuid.UUID `exhaustruct:"optional"`
	// Status is the current lifecycle state.
	Status ConversationStatus
	// Question is the initial question text supplied by the agent.
	Question string
	// Title is the short agent-written list title; empty falls back to the
	// truncated question at read time.
	Title string `exhaustruct:"optional"`
	// Context is the agent-composed context packet (freeform).
	Context string `exhaustruct:"optional"`
	// Summary is the agent-curated one-line gist of the conversation, written at
	// write-time to power deterministic history search; empty until populated.
	Summary string `exhaustruct:"optional"`
	// Tags are normalized facet labels (lowercase, trimmed, deduped) curated by
	// the agent; nil/empty until populated.
	Tags []string `exhaustruct:"optional"`
	// Entities are normalized named things the conversation is about (systems,
	// services, files); nil/empty until populated.
	Entities []string `exhaustruct:"optional"`
	// CreatedAt is when the conversation was opened.
	CreatedAt time.Time
	// UpdatedAt is when the conversation was last modified.
	UpdatedAt time.Time
}

// NewConversation builds a Conversation, validating every invariant.
func NewConversation(
	id uuid.UUID,
	projectID uuid.UUID,
	askerMemberID uuid.UUID,
	question string,
) (Conversation, error) {
	if id == uuid.Nil {
		return Conversation{}, errs.InvalidError{Field: "id", Reason: nilUUIDReason}
	}

	if projectID == uuid.Nil {
		return Conversation{}, errs.InvalidError{Field: "project_id", Reason: nilUUIDReason}
	}

	if askerMemberID == uuid.Nil {
		return Conversation{}, errs.InvalidError{Field: "asker_member_id", Reason: nilUUIDReason}
	}

	if strings.TrimSpace(question) == "" {
		return Conversation{}, errs.InvalidError{Field: fieldQuestion, Reason: emptyReason}
	}

	if exceedsRunes(question, MaxQuestionRunes) {
		return Conversation{}, errs.InvalidError{Field: fieldQuestion, Reason: reasonQuestionTooLong}
	}

	now := time.Now().UTC()

	return Conversation{
		ID:            id,
		ProjectID:     projectID,
		AskerMemberID: askerMemberID,
		Status:        ConversationStatusOpen,
		Question:      question,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

// DisplayTitle returns the agent-written title, or a truncated question for
// conversations opened without one — the server never generates text, it
// only trims it.
func (c Conversation) DisplayTitle() string {
	return DisplayTitle(c.Title, c.Question)
}

// DisplayTitle resolves a conversation's display title from its raw title and
// question: the title when set, otherwise the question truncated to 60 runes.
// Exported so read paths that hold only the two strings (not the full
// aggregate) share the one source of truth instead of re-implementing the trim.
func DisplayTitle(title, question string) string {
	if title != "" {
		return title
	}

	const maxRunes = 60

	runes := []rune(question)
	if len(runes) <= maxRunes {
		return question
	}

	return string(runes[:maxRunes]) + "…"
}

// ConversationParticipant is a member EXPLICITLY added to a conversation
// (Option A: add_participant) so they receive the fan-out of every question and
// answer. The asker and the assigned responder are participants implicitly
// (from the Conversation fields) and are not stored here.
type ConversationParticipant struct {
	MemberID uuid.UUID
	// AddedBy is who added them (asker or an existing participant); zero if the
	// adder row was since deleted.
	AddedBy uuid.UUID `exhaustruct:"optional"`
	// AddedAt is when they were added.
	AddedAt time.Time
}
