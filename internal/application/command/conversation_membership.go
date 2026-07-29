// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// conversationParticipants lists a conversation's explicitly-added participants.
type conversationParticipants interface {
	ParticipantsByConversation(ctx context.Context, conversationID uuid.UUID) ([]model.ConversationParticipant, error)
}

// candidateChecker reports whether a member is a still-active contacted pool
// candidate on a conversation.
type candidateChecker interface {
	IsActiveCandidate(ctx context.Context, conversationID, memberID uuid.UUID) (bool, error)
}

// memberOnConversation authorizes a write against a conversation: the member
// must be the asker, the labeled responder, an explicitly-added participant,
// or a still-active contacted candidate. Returns errs.ForbiddenError otherwise.
// Shared by FollowUp and UploadAttachment (the distribution list is the same;
// replies and attachments come from participants, never strangers).
func memberOnConversation(
	ctx context.Context,
	conv model.Conversation,
	memberID uuid.UUID,
	participants conversationParticipants,
	candidates candidateChecker,
	action string,
) error {
	if memberID == uuid.Nil {
		return errs.ForbiddenError{Action: action + " (no identity)"}
	}

	if memberID == conv.AskerMemberID || memberID == conv.ResponderMemberID {
		return nil
	}

	added, err := participants.ParticipantsByConversation(ctx, conv.ID)
	if err != nil {
		return errs.InternalError{Err: fmt.Errorf("listing participants: %w", err)}
	}

	for _, p := range added {
		if p.MemberID == memberID {
			return nil
		}
	}

	active, err := candidates.IsActiveCandidate(ctx, conv.ID, memberID)
	if err != nil {
		return errs.InternalError{Err: fmt.Errorf("checking candidacy: %w", err)}
	}

	if active {
		return nil
	}

	return errs.ForbiddenError{Action: action + " (not a participant)"}
}
