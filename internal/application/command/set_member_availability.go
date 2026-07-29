// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// memberAvailabilitySetter persists a member's availability. *identity.MemberStore
// satisfies it.
type memberAvailabilitySetter interface {
	SetAvailability(ctx context.Context, id uuid.UUID, status model.MemberStatus, returnDate *time.Time) error
}

// SetMemberAvailabilityCommand flips a member between active and on_leave.
type SetMemberAvailabilityCommand struct {
	MemberID   uuid.UUID
	Status     model.MemberStatus
	ReturnDate *time.Time `exhaustruct:"optional"`
}

// SetMemberAvailabilityHandler handles SetMemberAvailabilityCommand.
type SetMemberAvailabilityHandler struct {
	members memberAvailabilitySetter
}

// MustNewSetMemberAvailabilityHandler builds a handler.
func MustNewSetMemberAvailabilityHandler(members memberAvailabilitySetter) SetMemberAvailabilityHandler {
	if members == nil {
		panic("SetMemberAvailabilityHandler requires a non-nil memberAvailabilitySetter")
	}

	return SetMemberAvailabilityHandler{members: members}
}

// Handle validates the target status (active or on_leave) and persists it. The
// return_date reminder is kept only for on_leave.
func (h SetMemberAvailabilityHandler) Handle(ctx context.Context, cmd SetMemberAvailabilityCommand) error {
	if cmd.Status != model.MemberStatusActive && cmd.Status != model.MemberStatusOnLeave {
		return errs.InvalidError{Field: "status", Reason: "must be 'active' or 'on_leave'"}
	}

	returnDate := cmd.ReturnDate
	if cmd.Status != model.MemberStatusOnLeave {
		returnDate = nil
	}

	if err := h.members.SetAvailability(ctx, cmd.MemberID, cmd.Status, returnDate); err != nil {
		return translateErr(err, "member")
	}

	return nil
}
