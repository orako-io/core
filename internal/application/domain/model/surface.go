// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"slices"
	"time"

	"github.com/google/uuid"
)

// SurfaceProviderDiscord is the provider discriminator for a Discord surface.
const SurfaceProviderDiscord = "discord"

// SurfaceProviderSlack is the provider discriminator for a Slack correlation
// surface: channel_id = the Slack channel, thread_id = the thread_ts.
const SurfaceProviderSlack = "slack"

// SurfaceProviderTelegram is the provider discriminator for a Telegram
// correlation surface: channel_id = the chat_id, thread_id = the bot's outbound
// message_id the responder must reply_to.
const SurfaceProviderTelegram = "telegram"

// SurfaceKindThread marks a platform group-thread surface (e.g. a private
// Discord thread in the project channel). Reused for the Slack/Telegram
// direct-ask correlation binding, whose (channel_id, thread_id) pair is the
// inbound reply key.
const SurfaceKindThread = "thread"

// ConversationSurface is one native platform surface of a conversation hub
// (hub-and-spoke phase 4): at most one per platform. Members in
// CoveredMemberIDs discuss on the surface natively and are excluded from the
// per-member DM fan-out for this conversation.
type ConversationSurface struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
	Provider       string
	Kind           string
	// ChannelID is the parent channel the thread lives in.
	ChannelID string
	// ThreadID identifies the thread itself — the inbound correlation key.
	ThreadID string
	// CoveredMemberIDs are the members this surface reaches (successfully
	// invited to the thread).
	CoveredMemberIDs []uuid.UUID `exhaustruct:"optional"`
	CreatedAt        time.Time   `exhaustruct:"optional"`
	// ArchivedAt is set when the conversation closed and the thread was
	// archived; an archived surface no longer receives fan-out posts.
	ArchivedAt *time.Time `exhaustruct:"optional"`
}

// Covers reports whether memberID is reached by this surface.
func (s ConversationSurface) Covers(memberID uuid.UUID) bool {
	return slices.Contains(s.CoveredMemberIDs, memberID)
}

// Origin is the provenance key stamped on messages ingested FROM this surface
// (e.g. "discord:thread:123..."), so the fan-out never echoes a message back
// onto the surface it came from.
func (s ConversationSurface) Origin() string {
	return s.Provider + ":" + s.Kind + ":" + s.ThreadID
}
