// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// openConv seeds a fakeConversationRepository with an open conversation.
func openConv(repo *fakeConversationRepository, projectID, askerID, targetID uuid.UUID, question string) model.Conversation {
	convID := uuid.New()
	conv, _ := model.NewConversation(convID, projectID, askerID, question)
	conv.ResponderMemberID = targetID
	repo.conversations[convID] = conv

	return conv
}

// newResolveHandler wires a ResolveConversationHandler over the shared convRepo.
func newResolveHandler(convRepo *fakeConversationRepository, bus *fakeEventBus) ResolveConversationHandler {
	return MustNewResolveConversationHandler(convRepo, fakeTransactor{}, bus)
}

// fakeTransactor runs the unit of work inline, without a real transaction — the
// repositories it composes are in-memory fakes, so committing atomically is a
// no-op here; the test just needs fn to run.
type fakeTransactor struct{}

func (fakeTransactor) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

// TestResolveConversation_RejectsEmptySummary proves resolve enforces the same
// required-but-guided metadata contract as ask: no summary → guidance-bearing
// errs.InvalidError, and the conversation is left unresolved.
func TestResolveConversation_RejectsEmptySummary(t *testing.T) {
	t.Parallel()

	convRepo := newFakeConvRepo()
	bus := &fakeEventBus{}

	conv := openConv(convRepo, uuid.New(), uuid.New(), uuid.New(), "What does orako do?")
	h := newResolveHandler(convRepo, bus)

	_, err := h.Handle(t.Context(), ResolveConversationCommand{
		ConversationID: conv.ID,
		CloserMemberID: conv.AskerMemberID,
		Resolution:     "an answer",
		Tags:           []string{"topic"},
	})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) || invalid.Field != "summary" {
		t.Fatalf("want InvalidError on summary, got %v", err)
	}

	if got := convRepo.conversations[conv.ID]; got.Status == model.ConversationStatusResolved {
		t.Error("conversation must not be resolved when metadata is rejected")
	}
}

// TestResolveConversation_RejectsNoTags proves resolve rejects a summary with no tags.
func TestResolveConversation_RejectsNoTags(t *testing.T) {
	t.Parallel()

	convRepo := newFakeConvRepo()
	bus := &fakeEventBus{}

	conv := openConv(convRepo, uuid.New(), uuid.New(), uuid.New(), "What does orako do?")
	h := newResolveHandler(convRepo, bus)

	_, err := h.Handle(t.Context(), ResolveConversationCommand{
		ConversationID: conv.ID,
		CloserMemberID: conv.AskerMemberID,
		Resolution:     "an answer",
		Summary:        "a summary but no tags",
	})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) || invalid.Field != "tags" {
		t.Fatalf("want InvalidError on tags, got %v", err)
	}
}

// TestResolveConversation_InheritsConversationMetadata proves the dashboard /
// Connect-RPC resolve path (which supplies no summary/tags/entities) SUCCEEDS by
// inheriting the conversation's CURRENT curated metadata set at ask/follow_up
// time, and that the inherited facets are persisted onto the conversation.
func TestResolveConversation_InheritsConversationMetadata(t *testing.T) {
	t.Parallel()

	convRepo := newFakeConvRepo()
	projectID, askerID, targetID := uuid.New(), uuid.New(), uuid.New()

	conv := openConv(convRepo, projectID, askerID, targetID, "How do webhooks work?")
	// Curated at ask/follow_up time; already present on the conversation.
	conv.Summary = "webhook secret configured"
	conv.Tags = []string{"billing", "webhook"}
	conv.Entities = []string{"billing-webhook"}
	convRepo.conversations[conv.ID] = conv

	h := newResolveHandler(convRepo, &fakeEventBus{})

	// No summary/tags/entities on the command — the dashboard resolve shape.
	if _, err := h.Handle(t.Context(), ResolveConversationCommand{
		ConversationID: conv.ID,
		CloserMemberID: targetID,
		Resolution:     "set the DEPLOY_WEBHOOK_SECRET env var",
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// Conversation is resolved and keeps its curated metadata.
	stored := convRepo.conversations[conv.ID]
	if stored.Status != model.ConversationStatusResolved {
		t.Errorf("status = %q, want resolved", stored.Status)
	}

	if stored.Summary != "webhook secret configured" {
		t.Errorf("stored summary = %q, want inherited", stored.Summary)
	}

	if len(stored.Tags) != 2 {
		t.Errorf("stored tags = %v, want inherited 2", stored.Tags)
	}
}

// TestResolveConversation_AlreadyResolved proves resolving an already-resolved
// conversation is an InvalidError.
func TestResolveConversation_AlreadyResolved(t *testing.T) {
	t.Parallel()

	convRepo := newFakeConvRepo()
	projectID := uuid.New()
	askerID := uuid.New()

	conv := openConv(convRepo, projectID, askerID, uuid.New(), "question")
	convRepo.conversations[conv.ID] = model.Conversation{
		ID:        conv.ID,
		ProjectID: projectID,
		Status:    model.ConversationStatusResolved,
	}

	h := newResolveHandler(convRepo, &fakeEventBus{})

	_, err := h.Handle(t.Context(), ResolveConversationCommand{
		Summary:        "test summary",
		Tags:           []string{"topic"},
		ConversationID: conv.ID,
		CloserMemberID: askerID,
		Resolution:     "some resolution",
	})
	if err == nil {
		t.Fatal("expected error when resolving an already-resolved conversation")
	}

	var inv errs.InvalidError
	if !errors.As(err, &inv) {
		t.Errorf("want InvalidError, got %T: %v", err, err)
	}
}

// TestResolveConversation_WriteFails_ConversationNotResolved verifies atomicity:
// if a write inside the transaction fails, the conversation remains unresolved —
// no orphaned half-resolved conversation.
func TestResolveConversation_WriteFails_ConversationNotResolved(t *testing.T) {
	t.Parallel()

	convRepo := newFakeConvRepo()
	convRepo.updateErr = errors.New("simulated write failure")
	projectID := uuid.New()
	askerID := uuid.New()

	conv := openConv(convRepo, projectID, askerID, uuid.New(), "Will this orphan?")

	h := newResolveHandler(convRepo, &fakeEventBus{})

	_, err := h.Handle(t.Context(), ResolveConversationCommand{
		Summary:        "test summary",
		Tags:           []string{"topic"},
		ConversationID: conv.ID,
		CloserMemberID: askerID,
		Resolution:     "some answer",
	})
	if err == nil {
		t.Fatal("expected error when the write fails")
	}

	storedConv := convRepo.conversations[conv.ID]
	if storedConv.Status == model.ConversationStatusResolved {
		t.Error("conversation was marked resolved despite write failure — orphan detected")
	}
}

// TestResolveConversation_ResolutionBecomesAnswerMessage is the fix for the
// "closed but unanswered" live bug: the dashboard reply (resolution) must land
// in the thread as an answer message and publish MESSAGE_POSTED, so the asking
// agent (get_conversation, inline wait) actually sees it.
func TestResolveConversation_ResolutionBecomesAnswerMessage(t *testing.T) {
	t.Parallel()

	convRepo := newFakeConvRepo()
	bus := &fakeEventBus{}

	projectID := uuid.New()
	askerID := uuid.New()
	targetID := uuid.New()

	conv := openConv(convRepo, projectID, askerID, targetID, "Plausible ou PostHog ?")

	h := newResolveHandler(convRepo, bus)

	const resolution = "Plausible at launch; add Clarity once there is real traffic."

	if _, err := h.Handle(t.Context(), ResolveConversationCommand{
		Summary:        "test summary",
		Tags:           []string{"topic"},
		ConversationID: conv.ID,
		CloserMemberID: targetID,
		Resolution:     resolution,
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	answers := convRepo.messages[conv.ID]
	if len(answers) != 1 {
		t.Fatalf("answer messages persisted = %d, want 1", len(answers))
	}

	answer := answers[0]
	if answer.Role != model.MessageRoleAnswer || answer.Body != resolution || answer.AuthorMemberID != targetID {
		t.Errorf("answer message = %+v, want role=answer body=resolution author=responder", answer)
	}

	// CONVERSATION_CLOSED and MESSAGE_POSTED(answer) are published.
	if bus.countOfType(orakov1.EventType_EVENT_TYPE_CONVERSATION_CLOSED) != 1 {
		t.Error("ConversationClosed event not published")
	}

	var sawMessagePosted bool

	for _, env := range bus.published {
		if env.GetType() == orakov1.EventType_EVENT_TYPE_MESSAGE_POSTED {
			sawMessagePosted = true

			if env.GetMessagePosted().GetRole() != orakov1.MessageRole_MESSAGE_ROLE_ANSWER {
				t.Errorf("MESSAGE_POSTED role = %v, want ANSWER", env.GetMessagePosted().GetRole())
			}
		}
	}

	if !sawMessagePosted {
		t.Error("MESSAGE_POSTED(answer) was not published")
	}
}
