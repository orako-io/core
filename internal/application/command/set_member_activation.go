// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// memberActivationSetter persists a member's activation state. *identity.MemberStore
// satisfies it.
type memberActivationSetter interface {
	SetActivation(ctx context.Context, id uuid.UUID, status model.MemberStatus) error
}

// SetMemberActivationCommand deactivates (off billing) or reactivates a member.
type SetMemberActivationCommand struct {
	MemberID uuid.UUID
	Active   bool
}

// SetMemberActivationHandler handles SetMemberActivationCommand.
//
// Deactivation excludes the member from seat usage. Reactivation restores the
// member to active.
type SetMemberActivationHandler struct {
	members memberActivationSetter
}

// MustNewSetMemberActivationHandler builds a handler.
func MustNewSetMemberActivationHandler(members memberActivationSetter) SetMemberActivationHandler {
	if members == nil {
		panic("SetMemberActivationHandler requires a non-nil memberActivationSetter")
	}

	return SetMemberActivationHandler{members: members}
}

// Handle flips the member to active or deactivated.
func (h SetMemberActivationHandler) Handle(ctx context.Context, cmd SetMemberActivationCommand) error {
	status := model.MemberStatusDeactivated
	if cmd.Active {
		status = model.MemberStatusActive
	}

	if err := h.members.SetActivation(ctx, cmd.MemberID, status); err != nil {
		return translateErr(err, "member")
	}

	return nil
}
