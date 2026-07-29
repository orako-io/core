// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"unicode/utf8"

	"github.com/orako-io/core/internal/pkg/errs"
)

// nilUUIDReason is the shared validation reason for nil-UUID inputs.
const nilUUIDReason = "must not be the nil UUID"

// emptyReason is the shared validation reason for empty string inputs.
const emptyReason = "must not be empty"

// fieldQuestion is the validation field name for a question input, shared by
// the conversation validator.
const fieldQuestion = "question"

// Length caps on the free-text conversation fields, counted in Unicode
// runes (characters), not bytes. They bound the authenticated MCP write surface
// — an unbounded body is a resource-exhaustion vector (it is read into memory,
// persisted) — and are surfaced verbatim in the MCP
// tool descriptions so agents know the limit before sending.
const (
	// MaxQuestionRunes caps a conversation's question.
	MaxQuestionRunes = 10_000
	// MaxContextRunes caps the agent-composed context packet, which is larger
	// than a question (it may carry file snippets, symbols, ticket text).
	MaxContextRunes = 100_000
	// MaxMessageRunes caps a thread message body (follow-up, answer, resolution).
	MaxMessageRunes = 10_000
	// MaxAnswerRunes caps a distilled resolution answer.
	MaxAnswerRunes = 10_000
)

// Over-length validation reasons (kept in sync with the caps above).
const (
	reasonQuestionTooLong = "question is too long (max 10000 characters)"
	reasonContextTooLong  = "context is too long (max 100000 characters)"
	reasonMessageTooLong  = "message is too long (max 10000 characters)"
	reasonAnswerTooLong   = "answer is too long (max 10000 characters)"
)

// exceedsRunes reports whether s is longer than maxRunes characters.
func exceedsRunes(s string, maxRunes int) bool {
	return utf8.RuneCountInString(s) > maxRunes
}

// ValidateContextLen enforces MaxContextRunes on a conversation's context
// packet. Exported because the context is attached to a Conversation after
// construction (NewConversation takes only the question), so callers that set
// Context must validate it explicitly.
func ValidateContextLen(context string) error {
	if exceedsRunes(context, MaxContextRunes) {
		return errs.InvalidError{Field: "context", Reason: reasonContextTooLong}
	}

	return nil
}

// strUnspecified is the canonical string representation for zero/unspecified enum values.
const strUnspecified = "unspecified"

// Role is the function a member holds within a project. A member may hold
// several roles. Values mirror the orako.v1.Role proto enum numbering.
type Role int32

// Recognized role values. Values mirror orako.v1.Role.
const (
	RoleUnspecified Role = 0
	RoleDev         Role = 1 // asks questions through an agent
	RoleSpecialist  Role = 2 // answers (PO/PM/DA/senior dev)
	RoleLead        Role = 3 // admin over a project
	RoleAdmin       Role = 4 // org admin
)

// String returns the lowercase text stored in the database.
func (r Role) String() string {
	switch r {
	case RoleUnspecified:
		return strUnspecified
	case RoleDev:
		return "dev"
	case RoleSpecialist:
		return "specialist"
	case RoleLead:
		return "lead"
	case RoleAdmin:
		return "admin"
	}

	return strUnspecified
}

// MemberTransition is a point in a member's lifecycle. Emitted append-only so
// membership carries a free audit trail. Values mirror orako.v1.MemberTransition.
type MemberTransition int32

// Recognized member-transition values. Values mirror orako.v1.MemberTransition.
const (
	MemberTransitionUnspecified MemberTransition = 0
	MemberTransitionInvited     MemberTransition = 1
	MemberTransitionAccepted    MemberTransition = 2
	MemberTransitionRoleChanged MemberTransition = 3
	MemberTransitionSuspended   MemberTransition = 4
	MemberTransitionRemoved     MemberTransition = 5 // soft-delete (archive)
	MemberTransitionPurged      MemberTransition = 6 // hard-delete of PII, on legal request
)

// MessageRole distinguishes who authored a conversation message. Values mirror
// orako.v1.MessageRole.
type MessageRole int32

// Recognized message-role values. Values mirror orako.v1.MessageRole.
const (
	MessageRoleUnspecified MessageRole = 0
	MessageRoleQuestion    MessageRole = 1
	MessageRoleAnswer      MessageRole = 2
	MessageRoleFollowUp    MessageRole = 3
	MessageRoleSystem      MessageRole = 4 // routing/timeout notes
	// MessageRoleSecondOpinion marks a losing pool candidate's reply on an
	// already-claimed conversation, captured (rather than rejected) as an
	// attributed, non-authoritative addition to the thread: it never flips
	// conversations.status and is never redelivered as a question.
	MessageRoleSecondOpinion MessageRole = 5
)

// String returns the lowercase text stored in the database.
func (r MessageRole) String() string {
	switch r {
	case MessageRoleUnspecified:
		return strUnspecified
	case MessageRoleQuestion:
		return "question"
	case MessageRoleAnswer:
		return "answer"
	case MessageRoleFollowUp:
		return "follow_up"
	case MessageRoleSystem:
		return "system"
	case MessageRoleSecondOpinion:
		return "second_opinion"
	}

	return strUnspecified
}

// MessageSource records how a message was authored, so the UI can distinguish a
// message a person typed from one their coding agent posted on their behalf.
// Stored as the lowercase text in the messages.source column.
type MessageSource string

// Recognized message-source values.
const (
	// MessageSourceHuman is a message a person typed directly — in the dashboard
	// composer or a chat provider (Discord/Slack/…). The default.
	MessageSourceHuman MessageSource = "human"
	// MessageSourceAgent is a message a coding agent posted through the MCP tools
	// on the member's behalf (ask / follow_up / resolve_conversation).
	// The UI marks these "Name (via agent)".
	MessageSourceAgent MessageSource = "agent"
	// MessageSourceSystem is a routing/escalation note Orako itself wrote.
	MessageSourceSystem MessageSource = "system"
)

// Valid reports whether s is a recognized source; empty/unknown is treated as
// human by callers (the conservative default — never falsely claim agent).
func (s MessageSource) Valid() bool {
	switch s {
	case MessageSourceHuman, MessageSourceAgent, MessageSourceSystem:
		return true
	}

	return false
}
