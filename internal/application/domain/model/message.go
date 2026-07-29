// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// Message is an append-only entry in a conversation thread. Responder answers
// arrive here via the Slack webhook (Role == MessageRoleAnswer).
// AuthorMemberID is zero (uuid.Nil) for system messages.
type Message struct {
	// ID uniquely identifies the message.
	ID uuid.UUID
	// ConversationID links the message to its parent conversation.
	ConversationID uuid.UUID
	// AuthorMemberID identifies who wrote the message; zero for system messages.
	AuthorMemberID uuid.UUID
	// Role classifies the message within the conversation thread.
	Role MessageRole
	// Body is the message text.
	Body string
	// Source records how the message was authored (human/agent/system) so the UI
	// can mark an agent-posted message distinctly from one a person typed.
	Source MessageSource
	// AgentClient is the MCP client that authored an agent message (e.g.
	// "claude-code"); empty for human/system or an undeclared client. Only shown
	// when Source is agent — drives the "Name · Claude Code" label + logo.
	AgentClient string `exhaustruct:"optional"`
	// CreatedAt is when the message was appended.
	CreatedAt time.Time
	// Attachments are the files/images carried by this message; empty for a
	// text-only message. Populated by reads that join the attachments table,
	// not by NewMessage.
	Attachments []Attachment `exhaustruct:"optional"`
}

// NewMessage builds a Message, validating every invariant.
// authorMemberID may be uuid.Nil only when role is MessageRoleSystem.
// An empty or unrecognized source defaults to MessageSourceHuman — the
// conservative attribution that never falsely marks a message as agent-posted.
func NewMessage(
	id uuid.UUID,
	conversationID uuid.UUID,
	authorMemberID uuid.UUID,
	role MessageRole,
	body string,
	source MessageSource,
) (Message, error) {
	if id == uuid.Nil {
		return Message{}, errs.InvalidError{Field: "id", Reason: nilUUIDReason}
	}

	if conversationID == uuid.Nil {
		return Message{}, errs.InvalidError{Field: "conversation_id", Reason: nilUUIDReason}
	}

	if role == MessageRoleUnspecified {
		return Message{}, errs.InvalidError{Field: "role", Reason: "must be specified"}
	}

	if role != MessageRoleSystem && authorMemberID == uuid.Nil {
		return Message{}, errs.InvalidError{Field: "author_member_id", Reason: "required for non-system messages"}
	}

	if strings.TrimSpace(body) == "" {
		return Message{}, errs.InvalidError{Field: "body", Reason: emptyReason}
	}

	if exceedsRunes(body, MaxMessageRunes) {
		return Message{}, errs.InvalidError{Field: "body", Reason: reasonMessageTooLong}
	}

	if !source.Valid() {
		source = MessageSourceHuman
	}

	return Message{
		ID:             id,
		ConversationID: conversationID,
		AuthorMemberID: authorMemberID,
		Role:           role,
		Body:           body,
		Source:         source,
		CreatedAt:      time.Now().UTC(),
	}, nil
}
