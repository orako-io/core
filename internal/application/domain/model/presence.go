// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// Presence records a member's connection-presence state (online / offline).
// This is an ephemeral availability signal used for routing (Orako checks
// whether a responder is online before routing a question); it is NOT file
// activity.
type Presence struct {
	// MemberID identifies the member whose presence this records.
	MemberID uuid.UUID
	// Online is true when the member has an active connection.
	Online bool
	// UpdatedAt is when the presence was last refreshed.
	UpdatedAt time.Time
}

// PresenceFreshness is how recent a heartbeat must be to still count as
// online. Presence decays to offline by itself: nothing ever has to write
// online=false, closing a laptop is enough.
const PresenceFreshness = 90 * time.Second

// OnlineNow reports liveness with decay: the Online flag only counts while
// the last heartbeat is fresh.
func (p Presence) OnlineNow() bool {
	return p.Online && time.Since(p.UpdatedAt) <= PresenceFreshness
}

// NewPresence builds a Presence record, validating every invariant.
func NewPresence(memberID uuid.UUID, online bool, updatedAt time.Time) (Presence, error) {
	if memberID == uuid.Nil {
		return Presence{}, errs.InvalidError{Field: "member_id", Reason: nilUUIDReason}
	}

	if updatedAt.IsZero() {
		return Presence{}, errs.InvalidError{Field: "updated_at", Reason: "must be set"}
	}

	return Presence{
		MemberID:  memberID,
		Online:    online,
		UpdatedAt: updatedAt,
	}, nil
}
