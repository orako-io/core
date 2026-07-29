// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// memberBinder reads and updates a member's channel binding.
// *identity.MemberStore satisfies it.
type memberBinder interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Member, error)
	Update(ctx context.Context, member model.Member) error
}

// BindMemberChannelCommand sets exactly one member's external chat binding —
// the write behind a member's self-service OAuth connect (e.g. Discord
// identify). The caller is trusted to have authenticated the member out of band
// (the OAuth install carried a signed, single-use state for this member id).
type BindMemberChannelCommand struct {
	MemberID   uuid.UUID
	Channel    string // "discord" (extensible)
	ExternalID string // the provider's user id (e.g. Discord snowflake)
}

// BindMemberChannelHandler handles BindMemberChannelCommand.
type BindMemberChannelHandler struct {
	members memberBinder
}

// MustNewBindMemberChannelHandler builds a handler. It panics on nil
// dependencies.
func MustNewBindMemberChannelHandler(members memberBinder) BindMemberChannelHandler {
	if members == nil {
		panic("BindMemberChannelHandler requires a non-nil member store")
	}

	return BindMemberChannelHandler{members: members}
}

// Handle stores the external id on the member's matching binding field.
func (h BindMemberChannelHandler) Handle(ctx context.Context, cmd BindMemberChannelCommand) error {
	if cmd.ExternalID == "" {
		return errs.InvalidError{Field: "external_id", Reason: "must not be empty"}
	}

	member, err := h.members.ByID(ctx, cmd.MemberID)
	if err != nil {
		return translateErr(err, "member")
	}

	switch cmd.Channel {
	case "discord":
		member.DiscordUserID = cmd.ExternalID
	default:
		return errs.InvalidError{Field: "channel", Reason: fmt.Sprintf("unsupported channel %q for self-service binding", cmd.Channel)}
	}

	if err := h.members.Update(ctx, member); err != nil {
		return translateErr(err, "member")
	}

	return nil
}
