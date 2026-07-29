// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"time"

	"github.com/google/uuid"
)

// ProviderMessageState is the lifecycle of one delivered pool DM. Mirrors the
// `state` column CHECK on provider_messages.
type ProviderMessageState string

// Recognized provider message states.
const (
	// ProviderMessageStateReserving marks a row written BEFORE its Deliver
	// call, closing the double-delivery retry window: a row already present
	// for a candidate — in ANY state, including reserving — is what the
	// notified-set / per-row state guards already treat as "already
	// handled", so a whole-message retry that finds a reserving row never
	// re-enters Deliver for it. See delivery_notifier.go and
	// bridge_projector.go for the documented crash-window tradeoff this
	// establishes (a row stuck in reserving is never retried; the
	// escalation sweeper is the safety net for a genuinely missed
	// candidate).
	ProviderMessageStateReserving   ProviderMessageState = "reserving"
	ProviderMessageStatePosted      ProviderMessageState = "posted"
	ProviderMessageStateClaimedWon  ProviderMessageState = "claimed_won"
	ProviderMessageStateClaimedLost ProviderMessageState = "claimed_lost"
	ProviderMessageStateReleased    ProviderMessageState = "released"
	ProviderMessageStateResolved    ProviderMessageState = "resolved"
	ProviderMessageStateFailed      ProviderMessageState = "failed"
)

// ProviderMessage is one row of the delivery ledger: what got posted where,
// to whom, for which conversation, and its current lifecycle state. The pool
// dispatch fan-out writes one row per candidate DM; the ledger is what a
// later edit (claim/release/closure projection) or an inbound reply
// correlates against.
type ProviderMessage struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
	MemberID       uuid.UUID
	ProviderKind   string
	ChannelID      string
	MessageRef     string
	State          ProviderMessageState
	// CreatedAt / UpdatedAt are store-managed (DB defaults); a write-path
	// caller never sets them, only a read populates them.
	CreatedAt time.Time `exhaustruct:"optional"`
	UpdatedAt time.Time `exhaustruct:"optional"`
}
