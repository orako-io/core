// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"time"

	"github.com/google/uuid"
)

// Candidate is one member of a pool-dispatched conversation's candidate set.
// A candidate may claim the conversation until they are excluded (a silent
// release removes the silent responder from the pool for good).
type Candidate struct {
	MemberID  uuid.UUID
	InvitedAt time.Time
	// ExcludedAt is set when the candidate can no longer claim (released for
	// silence, or manually withdrawn).
	ExcludedAt *time.Time `exhaustruct:"optional"`
}

// Active reports whether the candidate may still claim.
func (c Candidate) Active() bool { return c.ExcludedAt == nil }
