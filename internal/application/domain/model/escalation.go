// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"time"

	"github.com/google/uuid"
)

// DefaultClaimTimeout is how long a pool conversation may sit unanswered
// before its candidates are nudged once. (The name keeps the stored/wire
// field's "claim" spelling; the claim model itself is gone — hub-and-spoke
// phase 2.)
const DefaultClaimTimeout = 30 * time.Minute

// DefaultAlertTimeout is how long a pool conversation may sit unanswered before
// the second escalation rung posts once to the alert channel.
const DefaultAlertTimeout = 4 * time.Hour

// ExpiryAlertMultiplier scales the org's effective alert timeout into the expiry
// timeout, the third and final escalation rung: an open, unanswered pool
// conversation older than (alert timeout × this) is marked timed_out. Reusing
// the alert timeout — rather than adding a separate org knob — means a shop that
// lengthened its alert timeout moves expiry out with it, and one that disabled
// alerts (alert timeout 0) disables auto-expiry too. 6 keeps the arithmetic
// simple and the default humane.
const ExpiryAlertMultiplier = 6

// DefaultExpiryTimeout is the product-default expiry threshold: 6 × the 4h
// default alert = 24h. A pool question nobody answered in a full day is treated
// as abandoned (status timed_out). Derived, not independently tunable — see
// ExpiryAlertMultiplier.
const DefaultExpiryTimeout = DefaultAlertTimeout * ExpiryAlertMultiplier

// OrgEscalationSettings are one organization's escalation knobs. A nil field
// means "no explicit choice" — the product default applies.
type OrgEscalationSettings struct {
	ClaimTimeoutSeconds *int64
	// AlertTimeoutSeconds is the channel-alert escalation rung (nil = product
	// default, 0 = disabled for the org).
	AlertTimeoutSeconds *int64
	// DefaultAlertChannelID is the org-wide fallback alert channel, used when
	// a project's provider has no alert_channel_id of its own.
	DefaultAlertChannelID string
}

// EffectiveClaimTimeoutSeconds resolves the claim timeout: stored value, else
// the product default. Zero means the nudge rule is disabled.
func (s OrgEscalationSettings) EffectiveClaimTimeoutSeconds() int64 {
	if s.ClaimTimeoutSeconds != nil {
		return *s.ClaimTimeoutSeconds
	}

	return int64(DefaultClaimTimeout / time.Second)
}

// EffectiveAlertTimeoutSeconds resolves the alert timeout: stored value, else
// the product default. Zero means the channel-alert rule is disabled.
func (s OrgEscalationSettings) EffectiveAlertTimeoutSeconds() int64 {
	if s.AlertTimeoutSeconds != nil {
		return *s.AlertTimeoutSeconds
	}

	return int64(DefaultAlertTimeout / time.Second)
}

// AlertCandidate is one unanswered pool conversation past its org's alert
// timeout, with everything the third escalation rung needs to post exactly
// once: both channel candidates (project's own, org default fallback) and how
// many active candidates are still waiting on it.
type AlertCandidate struct {
	ConversationID uuid.UUID
	ProjectID      uuid.UUID
	Question       string
	// Title is the agent-written short title; empty falls back to the
	// truncated question, same convention as Conversation.DisplayTitle.
	Title     string `exhaustruct:"optional"`
	CreatedAt time.Time
	// ProjectAlertChannelIDs is project_providers.alert_channel_ids; empty means
	// the project has none configured.
	ProjectAlertChannelIDs []string `exhaustruct:"optional"`
	// OrgAlertChannelID is the org-wide fallback, used when
	// ProjectAlertChannelIDs is empty.
	OrgAlertChannelID string `exhaustruct:"optional"`
	// ProjectAlertKind is the provider kind (e.g. "slack", "discord") of the
	// project_providers row the query selected — the same row
	// ProjectAlertChannelIDs comes from. The caller must resolve the
	// ChannelPoster by this exact (ProjectID, ProjectAlertKind) pair, whether
	// posting to ProjectAlertChannelIDs or falling back to OrgAlertChannelID —
	// never by "the project's provider" (review fit#4: a project with several
	// kinds configured has no single unambiguous "the" provider). Empty only
	// when the project has no provider configured at all.
	ProjectAlertKind string `exhaustruct:"optional"`
	// CandidateCount is the number of still-active (non-excluded) candidates.
	CandidateCount int64
}

// DisplayTitle returns the agent-written title, or a truncated question when
// none was supplied. Mirrors Conversation.DisplayTitle.
func (a AlertCandidate) DisplayTitle() string {
	if a.Title != "" {
		return a.Title
	}

	const maxRunes = 60

	runes := []rune(a.Question)
	if len(runes) <= maxRunes {
		return a.Question
	}

	return string(runes[:maxRunes]) + "…"
}

// AlertChannelIDs resolves the channels to post to: the project's own alert
// channels first, the org default as fallback. Empty means neither is
// configured — the caller must skip silently.
func (a AlertCandidate) AlertChannelIDs() []string {
	if len(a.ProjectAlertChannelIDs) > 0 {
		return a.ProjectAlertChannelIDs
	}

	if a.OrgAlertChannelID != "" {
		return []string{a.OrgAlertChannelID}
	}

	return nil
}
