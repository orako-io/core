// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// MemberStatus is the lifecycle state of a member account. Mirrors the `status`
// column values in the members table.
type MemberStatus string

// Recognized member status values.
const (
	MemberStatusInvited MemberStatus = "invited"
	// MemberStatusPending is a self-provisioned member who redeemed an org join
	// code at the MCP OAuth consent screen. They may connect and ask (they hold a
	// login identity), but are NOT routable — they never receive routed questions
	// until an admin approves them. Unlike `invited`, a pending member must never
	// auto-promote on login (see jwt_auth.acceptInvitation).
	MemberStatusPending     MemberStatus = "pending"
	MemberStatusActive      MemberStatus = "active"
	MemberStatusOnLeave     MemberStatus = "on_leave"    // temporarily away: not routable, still a seat
	MemberStatusDeactivated MemberStatus = "deactivated" // reversible off-boarding: not routable, not a seat
	MemberStatusSuspended   MemberStatus = "suspended"
	MemberStatusRemoved     MemberStatus = "removed" // soft-delete
	MemberStatusPurged      MemberStatus = "purged"  // PII hard-deleted on legal request
)

// Routable reports whether the agent may send this member questions. Only an
// active member is routable; on_leave, deactivated, invited, pending, suspended,
// removed and purged are all excluded from routing. A pending self-join member
// is silent by construction until an admin approves (activates) them.
func (s MemberStatus) Routable() bool { return s == MemberStatusActive }

// CanAuthenticate reports whether a member in this status may establish an
// authenticated session / resolve to an identity. Off-boarded (removed, purged,
// deactivated) and suspended members are denied — even though in local mode the
// account password may still verify and in oidc mode the IdP identity still
// exists. active, invited (pending first-login acceptance), pending (self-joined
// via an org join code, awaiting admin approval) and on_leave pass.
// This is the offboarding revocation boundary (H2); it must be enforced on every
// auth path, not just the MCP-token path.
func (s MemberStatus) CanAuthenticate() bool {
	switch s {
	case MemberStatusRemoved, MemberStatusPurged, MemberStatusDeactivated, MemberStatusSuspended:
		return false
	case MemberStatusActive, MemberStatusInvited, MemberStatusOnLeave, MemberStatusPending:
		return true
	default:
		return false // unknown status: fail closed
	}
}

// Billable reports whether this member counts as a paid seat. Deactivated,
// removed and purged members are off the seat count; everyone else (including
// on_leave) is billable.
func (s MemberStatus) Billable() bool {
	return s != MemberStatusDeactivated && s != MemberStatusRemoved && s != MemberStatusPurged
}

// DeliveryChannel is the member's preferred contact channel: where Orako reaches
// them when an agent asks them a question. Mirrors the `delivery_channel` column.
type DeliveryChannel string

// Recognized delivery channel values.
const (
	DeliveryChannelSlack     DeliveryChannel = "slack"
	DeliveryChannelTeams     DeliveryChannel = "teams"
	DeliveryChannelTelegram  DeliveryChannel = "telegram"
	DeliveryChannelDiscord   DeliveryChannel = "discord"
	DeliveryChannelDashboard DeliveryChannel = "dashboard" // default; needs no external binding
)

// Valid reports whether c is a recognized delivery channel.
func (c DeliveryChannel) Valid() bool {
	switch c {
	case DeliveryChannelSlack, DeliveryChannelTeams, DeliveryChannelTelegram, DeliveryChannelDiscord, DeliveryChannelDashboard:
		return true
	default:
		return false
	}
}

// Member is the Orako-canonical identity of a participant. Connector IDs
// (slack_user_id, telegram_chat_id) are bindings resolved from a connector
// directory or set manually by an admin.
// Email may be empty after a PII purge (MemberStatusPurged).
type Member struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	// FirstName / LastName / GitHandle are optional profile fields used to match
	// the member to commits and mentions (routing context for the agent).
	FirstName string `exhaustruct:"optional"`
	LastName  string `exhaustruct:"optional"`
	GitHandle string `exhaustruct:"optional"`
	// AccountID links this member to the authenticated human account. The nil UUID
	// means the member is an external-only responder with no login identity (the
	// default for invited members sourced from Slack/Telegram).
	AccountID uuid.UUID `exhaustruct:"optional"`
	// SlackUserID is the Slack user ID binding; empty for Teams/Telegram projects.
	SlackUserID string `exhaustruct:"optional"`
	// TelegramChatID is the Telegram chat ID binding (the responder's numeric user
	// ID as a string). Empty for Slack/Teams projects. Bound manually by an admin;
	// the value is obtained when the responder first messages the bot.
	TelegramChatID string `exhaustruct:"optional"`
	// TeamsUserID is the Microsoft Teams DM binding (the Bot Framework
	// from.aadObjectId, an Azure AD object id).
	TeamsUserID string `exhaustruct:"optional"`
	// DiscordUserID is the Discord DM binding (the member's Discord user
	// snowflake).
	DiscordUserID string `exhaustruct:"optional"`
	// BindingError is the last delivery failure recorded on the member's bound
	// channel (empty = healthy). Cleared whenever the binding is re-saved.
	BindingError string `exhaustruct:"optional"`
	// DeliveryChannel is where Orako reaches this member. Defaults to
	// DeliveryChannelDashboard, which needs no external binding and always works.
	DeliveryChannel DeliveryChannel `exhaustruct:"optional"`
	Status          MemberStatus
	// ReturnDate is the expected return-from-leave reminder, set only while the
	// status is on_leave. Display-only: nothing reactivates the member on it.
	ReturnDate *time.Time `exhaustruct:"optional"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// BindingFor reports whether the member has the binding value required by
// channel c. The dashboard channel needs no binding and is always "bound".
func (m Member) BindingFor(c DeliveryChannel) string {
	switch c {
	case DeliveryChannelSlack:
		return m.SlackUserID
	case DeliveryChannelTelegram:
		return m.TelegramChatID
	case DeliveryChannelTeams:
		return m.TeamsUserID
	case DeliveryChannelDiscord:
		return m.DiscordUserID
	case DeliveryChannelDashboard:
		return "bound"
	default:
		return ""
	}
}

// NewActiveMember builds an active, account-linked Member for the org-creator
// path. It requires a non-nil accountID and always sets Status=active and
// DeliveryChannel=dashboard. Email and DisplayName are left empty (the account
// holds the human identity; they can be filled in by the caller if available).
// Use NewMember for the standard invited-responder flow instead.
func NewActiveMember(id, accountID uuid.UUID) (Member, error) {
	if id == uuid.Nil {
		return Member{}, errs.InvalidError{Field: "id", Reason: nilUUIDReason}
	}

	if accountID == uuid.Nil {
		return Member{}, errs.InvalidError{Field: "account_id", Reason: nilUUIDReason}
	}

	now := time.Now().UTC()

	return Member{
		ID:              id,
		Email:           "",
		DisplayName:     "",
		AccountID:       accountID,
		DeliveryChannel: DeliveryChannelDashboard,
		Status:          MemberStatusActive,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

// NewPendingMember builds an account-linked Member in the pending self-join
// state: the person redeemed an org join code at the MCP OAuth consent screen.
// It requires a non-nil accountID and always sets Status=pending and
// DeliveryChannel=dashboard. email/displayName carry the human's identity from
// the account (either may be empty); a pending member can connect and ask but is
// never routable until an admin activates them.
func NewPendingMember(id, accountID uuid.UUID, email, displayName string) (Member, error) {
	if id == uuid.Nil {
		return Member{}, errs.InvalidError{Field: "id", Reason: nilUUIDReason}
	}

	if accountID == uuid.Nil {
		return Member{}, errs.InvalidError{Field: "account_id", Reason: nilUUIDReason}
	}

	if e := strings.TrimSpace(email); e != "" && !ValidEmail(e) {
		return Member{}, errs.InvalidError{Field: "email", Reason: "must be a valid email address"}
	}

	now := time.Now().UTC()

	return Member{
		ID:              id,
		Email:           email,
		DisplayName:     displayName,
		AccountID:       accountID,
		DeliveryChannel: DeliveryChannelDashboard,
		Status:          MemberStatusPending,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

// NewMember builds a Member ready for first insertion, validating every
// invariant. The caller supplies the canonical email; DisplayName is required.
func NewMember(id uuid.UUID, email, displayName string) (Member, error) {
	if id == uuid.Nil {
		return Member{}, errs.InvalidError{Field: "id", Reason: nilUUIDReason}
	}

	if strings.TrimSpace(displayName) == "" {
		return Member{}, errs.InvalidError{Field: "display_name", Reason: emptyReason}
	}

	// A non-empty email must be well-formed. Empty is allowed (a member can be
	// created without one, and it is blanked on a PII purge), but a malformed
	// one — e.g. a comma instead of a dot ("name@gmail,com") — must be rejected
	// at the door: left in, it becomes an undeliverable member whose invitation
	// email fails forever, and the notifier retries that poison message in a
	// tight loop (see the 2026-07 invite-loop incident).
	if e := strings.TrimSpace(email); e != "" && !ValidEmail(e) {
		return Member{}, errs.InvalidError{Field: "email", Reason: "must be a valid email address"}
	}

	now := time.Now().UTC()

	return Member{
		ID:              id,
		Email:           email,
		DisplayName:     displayName,
		DeliveryChannel: DeliveryChannelDashboard,
		Status:          MemberStatusInvited,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

// ValidEmail is a cheap shape check: a non-empty local part, an "@", and a dot
// in the domain part. It is deliberately lenient (the identity provider does
// the authoritative validation) — its only job is to reject obvious typos from
// a pasted list (a comma for a dot, a missing "@") before they become an
// undeliverable member row.
func ValidEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return false
	}

	return strings.IndexByte(s[at+1:], '.') > 0
}
