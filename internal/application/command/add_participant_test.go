// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/errs"
)

type fakeParticipantStore struct {
	existing []model.ConversationParticipant
	added    []model.ConversationParticipant
}

func (f *fakeParticipantStore) AddParticipant(_ context.Context, _, memberID, addedBy uuid.UUID) error {
	f.added = append(f.added, model.ConversationParticipant{MemberID: memberID, AddedBy: addedBy})

	return nil
}

func (f *fakeParticipantStore) ParticipantsByConversation(_ context.Context, _ uuid.UUID) ([]model.ConversationParticipant, error) {
	return f.existing, nil
}

type fakeProjectMembers struct{ members []uuid.UUID }

func (f *fakeProjectMembers) MembersByProject(_ context.Context, _ uuid.UUID) ([]repository.ProjectMembership, error) {
	out := make([]repository.ProjectMembership, len(f.members))
	for i, m := range f.members {
		out[i] = repository.ProjectMembership{ProjectID: uuid.Nil, MemberID: m, Role: model.RoleUnspecified, Domains: nil}
	}

	return out, nil
}

// seedOpenConversation seeds an open conversation with an asker and an assigned
// responder for the add_participant tests.
func seedOpenConversation(repo *fakeConversationRepository, askerID, targetID uuid.UUID) model.Conversation {
	conv := model.Conversation{
		ID:                uuid.New(),
		ProjectID:         uuid.New(),
		AskerMemberID:     askerID,
		ResponderMemberID: targetID,
		Status:            model.ConversationStatusOpen,
		Question:          "Which index fits this query?",
	}
	repo.conversations[conv.ID] = conv

	return conv
}

func TestAddParticipant_AskerAddsProjectMember(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	askerID, targetID, newID := uuid.New(), uuid.New(), uuid.New()
	conv := seedOpenConversation(repo, askerID, targetID)

	parts := &fakeParticipantStore{}
	members := &fakeProjectMembers{members: []uuid.UUID{askerID, targetID, newID}}
	bus := &fakeEventBus{}

	h := MustNewAddParticipantHandler(repo, parts, members, bus)

	res, err := h.Handle(t.Context(), AddParticipantCommand{
		ConversationID:  conv.ID,
		NewMemberID:     newID,
		AddedByMemberID: askerID, // the asker is on the conversation
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if res.AlreadyParticipant {
		t.Error("AlreadyParticipant should be false for a fresh add")
	}

	if len(parts.added) != 1 || parts.added[0].MemberID != newID {
		t.Fatalf("want the new member added, got %+v", parts.added)
	}

	if _, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_CONVERSATION_PARTICIPANT_ADDED); !ok {
		t.Error("want a CONVERSATION_PARTICIPANT_ADDED event published")
	}
}

func TestAddParticipant_NonParticipantCallerForbidden(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	askerID, targetID, newID := uuid.New(), uuid.New(), uuid.New()
	conv := seedOpenConversation(repo, askerID, targetID)

	parts := &fakeParticipantStore{}
	members := &fakeProjectMembers{members: []uuid.UUID{askerID, targetID, newID}}

	h := MustNewAddParticipantHandler(repo, parts, members, &fakeEventBus{})

	_, err := h.Handle(t.Context(), AddParticipantCommand{
		ConversationID:  conv.ID,
		NewMemberID:     newID,
		AddedByMemberID: uuid.New(), // a stranger, not on the conversation
	})

	var fe errs.ForbiddenError
	if !errors.As(err, &fe) {
		t.Fatalf("want ForbiddenError, got %v", err)
	}

	if len(parts.added) != 0 {
		t.Error("nothing should be added when the caller is forbidden")
	}
}

func TestAddParticipant_TargetNotProjectMember(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	askerID, targetID, newID := uuid.New(), uuid.New(), uuid.New()
	conv := seedOpenConversation(repo, askerID, targetID)

	parts := &fakeParticipantStore{}
	// newID deliberately absent from the project roster.
	members := &fakeProjectMembers{members: []uuid.UUID{askerID, targetID}}

	h := MustNewAddParticipantHandler(repo, parts, members, &fakeEventBus{})

	_, err := h.Handle(t.Context(), AddParticipantCommand{
		ConversationID:  conv.ID,
		NewMemberID:     newID,
		AddedByMemberID: askerID,
	})

	var ie errs.InvalidError
	if !errors.As(err, &ie) {
		t.Fatalf("want InvalidError for a non-project-member target, got %v", err)
	}
}

func TestAddParticipant_AlreadyParticipantIsNoop(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	askerID, targetID := uuid.New(), uuid.New()
	conv := seedOpenConversation(repo, askerID, targetID)

	parts := &fakeParticipantStore{}
	members := &fakeProjectMembers{members: []uuid.UUID{askerID, targetID}}
	bus := &fakeEventBus{}

	h := MustNewAddParticipantHandler(repo, parts, members, bus)

	// Adding the assigned responder, who is already an implicit participant.
	res, err := h.Handle(t.Context(), AddParticipantCommand{
		ConversationID:  conv.ID,
		NewMemberID:     targetID,
		AddedByMemberID: askerID,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if !res.AlreadyParticipant {
		t.Error("want AlreadyParticipant=true for a member already on the conversation")
	}

	if len(parts.added) != 0 {
		t.Error("no add should happen for an existing participant")
	}

	if _, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_CONVERSATION_PARTICIPANT_ADDED); ok {
		t.Error("no event should be published for an idempotent no-op")
	}
}
