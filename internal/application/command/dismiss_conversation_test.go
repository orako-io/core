// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// TestDismissConversation_OpenAndAnswered proves an open or answered
// conversation transitions to dismissed, with no KB side effect (the handler
// depends only on the conversation repository — there is no KB path to take).
func TestDismissConversation_OpenAndAnswered(t *testing.T) {
	t.Parallel()

	for _, start := range []model.ConversationStatus{model.ConversationStatusOpen, model.ConversationStatusAnswered} {
		t.Run(string(start), func(t *testing.T) {
			t.Parallel()

			repo := newFakeConvRepo()
			conv := openConv(repo, uuid.New(), uuid.New(), uuid.New(), "dead thread?")

			seeded := repo.conversations[conv.ID]
			seeded.Status = start
			repo.conversations[conv.ID] = seeded

			h := MustNewDismissConversationHandler(repo)

			res, err := h.Handle(t.Context(), DismissConversationCommand{
				ConversationID:    conv.ID,
				DismisserMemberID: uuid.New(),
			})
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}

			if res.Status != model.ConversationStatusDismissed {
				t.Errorf("result status = %q, want dismissed", res.Status)
			}

			if got := repo.conversations[conv.ID].Status; got != model.ConversationStatusDismissed {
				t.Errorf("stored status = %q, want dismissed", got)
			}
		})
	}
}

// TestDismissConversation_TerminalRejected proves a terminal conversation
// (closed, dismissed, timed_out) cannot be dismissed and is left untouched.
func TestDismissConversation_TerminalRejected(t *testing.T) {
	t.Parallel()

	for _, start := range []model.ConversationStatus{
		model.ConversationStatusResolved,
		model.ConversationStatusDismissed,
		model.ConversationStatusTimedOut,
	} {
		t.Run(string(start), func(t *testing.T) {
			t.Parallel()

			repo := newFakeConvRepo()
			conv := openConv(repo, uuid.New(), uuid.New(), uuid.New(), "already done")

			seeded := repo.conversations[conv.ID]
			seeded.Status = start
			repo.conversations[conv.ID] = seeded

			h := MustNewDismissConversationHandler(repo)

			_, err := h.Handle(t.Context(), DismissConversationCommand{
				ConversationID:    conv.ID,
				DismisserMemberID: uuid.New(),
			})

			var inv errs.InvalidError
			if !errors.As(err, &inv) {
				t.Fatalf("want InvalidError, got %T: %v", err, err)
			}

			if got := repo.conversations[conv.ID].Status; got != start {
				t.Errorf("status mutated to %q, want unchanged %q", got, start)
			}
		})
	}
}

// TestDismissConversation_NotFound proves an unknown conversation surfaces an
// error and writes nothing.
func TestDismissConversation_NotFound(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	h := MustNewDismissConversationHandler(repo)

	_, err := h.Handle(t.Context(), DismissConversationCommand{
		ConversationID:    uuid.New(),
		DismisserMemberID: uuid.New(),
	})
	if err == nil {
		t.Fatal("want error for unknown conversation, got nil")
	}
}
