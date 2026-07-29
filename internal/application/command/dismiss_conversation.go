// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/errs"
)

// DismissConversationCommand closes an unanswered conversation: the asker or a
// responder gave up on it. Unlike ResolveConversation it writes no resolution,
// no answer message, and publishes no closure events — there is nothing
// resolved to carry.
type DismissConversationCommand struct {
	ConversationID    uuid.UUID
	DismisserMemberID uuid.UUID
}

// DismissConversationResult carries the resulting status.
type DismissConversationResult struct {
	Status model.ConversationStatus
}

// DismissConversationHandler handles DismissConversationCommand.
type DismissConversationHandler struct {
	convRepo repository.ConversationRepository
}

// MustNewDismissConversationHandler builds a handler. It panics on a
// nil dependency, consistent with the other command handlers.
func MustNewDismissConversationHandler(convRepo repository.ConversationRepository) DismissConversationHandler {
	if convRepo == nil {
		panic("DismissConversationHandler requires a non-nil ConversationRepository")
	}

	return DismissConversationHandler{convRepo: convRepo}
}

// Handle transitions an open or answered conversation to dismissed. Any other
// starting status (closed, dismissed, timed_out) is rejected: a terminal
// conversation is immutable, and re-dismissing is a no-op the caller should not
// mistake for a fresh action.
func (h DismissConversationHandler) Handle(ctx context.Context, cmd DismissConversationCommand) (DismissConversationResult, error) {
	conv, err := h.convRepo.ConversationByID(ctx, cmd.ConversationID)
	if err != nil {
		return DismissConversationResult{}, translateErr(err, "conversation")
	}

	if conv.Status != model.ConversationStatusOpen && conv.Status != model.ConversationStatusAnswered {
		return DismissConversationResult{}, errs.InvalidError{
			Field:  fieldConversationID,
			Reason: "only an open or answered conversation can be dismissed",
		}
	}

	if err := h.convRepo.UpdateStatus(ctx, cmd.ConversationID, model.ConversationStatusDismissed); err != nil {
		return DismissConversationResult{}, translateErr(err, "conversation")
	}

	return DismissConversationResult{Status: model.ConversationStatusDismissed}, nil
}
