// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

// Expert is the read-side DTO for a routable project member. Every project
// member is a potential responder; the agent routes on Domains.
type Expert struct {
	MemberID    uuid.UUID
	DisplayName string
	Email       string
	// Status distinguishes pending invitations ("invited") from active members.
	Status model.MemberStatus
	// InvitedAt is the member row creation time (= when the invitation was sent).
	InvitedAt time.Time
	// Domains are the member's expertise tags; the agent routes on these.
	Domains []string
	// Online is a weak, poll-on-demand presence hint — never a hard gate.
	Online bool
	// DeliveryChannel is where Orako reaches this member (discord/slack/…). The
	// agent uses it to format its question for that platform and respect the
	// platform's message-length limit when composing the ask.
	DeliveryChannel model.DeliveryChannel
	// Mention is the provider-native mention token when one is available.
	Mention string `exhaustruct:"optional"`
}

// MessageView is the read-side DTO for a single conversation message.
type MessageView struct {
	ID uuid.UUID
	// AuthorMemberID is zero for system messages.
	AuthorMemberID uuid.UUID
	Role           model.MessageRole
	Body           string
	// Source records how the message was authored (human/agent/system) so the
	// dashboard can mark an agent-posted message "Name (via agent)".
	Source model.MessageSource
	// AgentClient is which coding agent authored an agent message (e.g.
	// "claude-code"); empty for human/system. Drives the agent label + logo.
	AgentClient string `exhaustruct:"optional"`
	CreatedAt   time.Time
	// Attachments are the files/images on this message, each with a short-lived
	// signed download URL. Empty for a text-only message.
	Attachments []AttachmentView `exhaustruct:"optional"`
}

// AttachmentView is one file/image on a message, with a signed URL the agent
// or dashboard uses to download it.
type AttachmentView struct {
	ID          uuid.UUID
	Filename    string
	MimeType    string
	SizeBytes   int64
	DownloadURL string
}

// ProjectSummary is the read-side DTO for one project the caller is a member
// of. Returned by ListProjects.
type ProjectSummary struct {
	ID   uuid.UUID
	Name string
	// Role is the caller's role in this project.
	Role model.Role
	// OrgID is the owning organization, for grouping in cross-org lists.
	OrgID uuid.UUID `exhaustruct:"optional"`
}

// ProjectDetail is the read-side DTO for one project's identity, archived
// status, aggregate stats, and per-provider alert routing. Backs the Projects
// tab. Returned by ListProjectsDetailed.
type ProjectDetail struct {
	ID                uuid.UUID
	Name              string
	Archived          bool
	MemberCount       int
	ConversationCount int
	ResolvedCount     int
	// Routing is the project's configured providers with their alert channels.
	Routing []service.ProviderAlertChannel `exhaustruct:"optional"`
}

// Participant is one person on a conversation, resolved with a display name so
// the UI renders avatars/names without extra lookups.
type Participant struct {
	MemberID    uuid.UUID
	DisplayName string
	// Role situates the participant: "asker", "specialist", or "added".
	Role string
}

// ConversationSummary is the read-side DTO for one entry in a conversations
// list. Returned by ListConversations.
type ConversationSummary struct {
	ID                uuid.UUID
	Status            model.ConversationStatus
	Question          string
	Title             string
	ResponderMemberID uuid.UUID
	CreatedAt         time.Time
	// ProjectID is which project this conversation belongs to — surfaced so a
	// multi-project (org-wide) list can render a per-row project chip.
	ProjectID uuid.UUID
	// ProjectName is the resolved name of ProjectID.
	ProjectName string `exhaustruct:"optional"`
	// Participants = asker, assigned responder (when any), then explicitly
	// added members — deduplicated, display names resolved.
	Participants []Participant `exhaustruct:"optional"`
}

// ConversationView is the composite read-side DTO for a conversation and its
// messages. Returned by GetConversation.
type ConversationView struct {
	ID                uuid.UUID
	ProjectID         uuid.UUID
	AskerMemberID     uuid.UUID
	ResponderMemberID uuid.UUID
	Status            model.ConversationStatus
	Question          string
	Title             string
	Messages          []MessageView
	CreatedAt         time.Time
	UpdatedAt         time.Time
	// Participants = asker, assigned responder (when any), then explicitly
	// added members — deduplicated, display names resolved.
	Participants []Participant `exhaustruct:"optional"`
}

// InboxItem is the read-side DTO for one pending question addressed to a
// responder. Returned by ListInbox.
type InboxItem struct {
	ConversationID uuid.UUID
	ProjectID      uuid.UUID
	Question       string
	Title          string
	AskerMemberID  uuid.UUID
	Status         model.ConversationStatus
	CreatedAt      time.Time
}

// MemberView is the caller-facing read-side DTO for a member's own contact
// settings. It carries bindings (identifiers), never credentials.
type MemberView struct {
	ID uuid.UUID
	// Email may be empty after a PII purge.
	Email           string
	DisplayName     string
	FirstName       string
	LastName        string
	GitHandle       string
	DeliveryChannel model.DeliveryChannel
	SlackUserID     string
	TelegramChatID  string
	TeamsUserID     string
	DiscordUserID   string
	// BindingError is the last delivery failure on the member's bound channel
	// (empty = healthy).
	BindingError string
	Status       model.MemberStatus
	// CreatedAt is the member row creation time (= when the invitation was sent).
	CreatedAt time.Time
}

// NewMemberView maps a domain Member to its caller-facing DTO.
func NewMemberView(m model.Member) MemberView {
	return MemberView{
		ID:              m.ID,
		Email:           m.Email,
		DisplayName:     m.DisplayName,
		FirstName:       m.FirstName,
		LastName:        m.LastName,
		GitHandle:       m.GitHandle,
		DeliveryChannel: m.DeliveryChannel,
		SlackUserID:     m.SlackUserID,
		TelegramChatID:  m.TelegramChatID,
		TeamsUserID:     m.TeamsUserID,
		DiscordUserID:   m.DiscordUserID,
		BindingError:    m.BindingError,
		Status:          m.Status,
		CreatedAt:       m.CreatedAt,
	}
}
