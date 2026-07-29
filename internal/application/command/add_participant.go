// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/errs"
)

// AddParticipantCommand adds a member to an existing conversation (Option A) so
// they receive the fan-out of every question and answer.
type AddParticipantCommand struct {
	ConversationID uuid.UUID
	// NewMemberID is the member being added as a participant.
	NewMemberID uuid.UUID
	// AddedByMemberID is the caller doing the adding — must already be on the
	// conversation (the asker, the assigned responder, or an existing participant).
	AddedByMemberID uuid.UUID
}

// AddParticipantResult reports the outcome.
type AddParticipantResult struct {
	// AlreadyParticipant is true when the member was already on the conversation
	// (idempotent no-op: no event is published).
	AlreadyParticipant bool
}

// participantStore is the participant read/write port. *conversation.Store
// satisfies it.
type participantStore interface {
	AddParticipant(ctx context.Context, conversationID, memberID, addedBy uuid.UUID) error
	ParticipantsByConversation(ctx context.Context, conversationID uuid.UUID) ([]model.ConversationParticipant, error)
}

// projectMembersReader lists a project's memberships, used to validate that the
// added member actually belongs to the conversation's project. *eventlog's
// project store satisfies it (same method the query layer uses).
type projectMembersReader interface {
	MembersByProject(ctx context.Context, projectID uuid.UUID) ([]repository.ProjectMembership, error)
}

// AddParticipantHandler handles AddParticipantCommand.
type AddParticipantHandler struct {
	convRepo       repository.ConversationRepository
	participants   participantStore
	projectMembers projectMembersReader
	bus            eventBus
}

// MustNewAddParticipantHandler builds the handler. It panics on nil deps.
func MustNewAddParticipantHandler(
	convRepo repository.ConversationRepository,
	participants participantStore,
	projectMembers projectMembersReader,
	bus eventBus,
) AddParticipantHandler {
	if convRepo == nil || participants == nil || projectMembers == nil || bus == nil {
		panic("AddParticipantHandler requires non-nil convRepo, participants, projectMembers, and bus")
	}

	return AddParticipantHandler{convRepo: convRepo, participants: participants, projectMembers: projectMembers, bus: bus}
}

// Handle validates and adds the participant, then publishes
// CONVERSATION_PARTICIPANT_ADDED so the projector delivers the thread history to
// the new participant. Rules: the conversation must be open/answered (not
// closed); the caller must already be on the conversation; the added member must
// belong to the conversation's project. Adding someone already on the
// conversation is an idempotent no-op (no event).
func (h AddParticipantHandler) Handle(ctx context.Context, cmd AddParticipantCommand) (AddParticipantResult, error) {
	if cmd.NewMemberID == uuid.Nil {
		return AddParticipantResult{}, errs.InvalidError{Field: "new_member_id", Reason: "must be specified"}
	}

	conv, err := h.convRepo.ConversationByID(ctx, cmd.ConversationID)
	if err != nil {
		return AddParticipantResult{}, translateErr(err, "conversation")
	}

	if conv.Status != model.ConversationStatusOpen && conv.Status != model.ConversationStatusAnswered {
		return AddParticipantResult{}, errs.InvalidError{Field: fieldConversationID, Reason: "conversation is closed"}
	}

	existing, err := h.participants.ParticipantsByConversation(ctx, cmd.ConversationID)
	if err != nil {
		return AddParticipantResult{}, errs.InternalError{Err: fmt.Errorf("listing participants: %w", err)}
	}

	set := effectiveParticipantSet(conv, existing)

	// The caller must already be on the conversation to add others.
	if !set[cmd.AddedByMemberID] {
		return AddParticipantResult{}, errs.ForbiddenError{Action: "add a participant (you are not on this conversation)"}
	}

	// Idempotent: already a participant (implicit asker/responder, or already added).
	if set[cmd.NewMemberID] {
		return AddParticipantResult{AlreadyParticipant: true}, nil
	}

	// The added member must belong to the conversation's project.
	members, err := h.projectMembers.MembersByProject(ctx, conv.ProjectID)
	if err != nil {
		return AddParticipantResult{}, errs.InternalError{Err: fmt.Errorf("listing project members: %w", err)}
	}

	if !slices.ContainsFunc(members, func(m repository.ProjectMembership) bool { return m.MemberID == cmd.NewMemberID }) {
		return AddParticipantResult{}, errs.InvalidError{Field: "new_member_id", Reason: "not a member of this conversation's project"}
	}

	if err := h.participants.AddParticipant(ctx, cmd.ConversationID, cmd.NewMemberID, cmd.AddedByMemberID); err != nil {
		return AddParticipantResult{}, translateErr(err, "participant")
	}

	if _, err := h.bus.Publish(ctx, &orakov1.Envelope{
		ProjectId: conv.ProjectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_CONVERSATION_PARTICIPANT_ADDED,
		Payload: &orakov1.Envelope_ConversationParticipantAdded{
			ConversationParticipantAdded: &orakov1.ConversationParticipantAdded{
				ConversationId:  cmd.ConversationID.String(),
				MemberId:        cmd.NewMemberID.String(),
				AddedByMemberId: cmd.AddedByMemberID.String(),
			},
		},
	}); err != nil {
		return AddParticipantResult{}, errs.InternalError{Err: fmt.Errorf("publishing participant_added: %w", err)}
	}

	return AddParticipantResult{AlreadyParticipant: false}, nil
}

// effectiveParticipantSet is the full set of members on a conversation: the
// asker and the assigned responder (implicit) plus every explicitly-added
// participant. Shared with the fan-out projector so "who is on this
// conversation" is computed identically everywhere.
func effectiveParticipantSet(conv model.Conversation, added []model.ConversationParticipant) map[uuid.UUID]bool {
	set := make(map[uuid.UUID]bool, len(added)+2)

	if conv.AskerMemberID != uuid.Nil {
		set[conv.AskerMemberID] = true
	}

	if conv.ResponderMemberID != uuid.Nil {
		set[conv.ResponderMemberID] = true
	}

	for _, p := range added {
		set[p.MemberID] = true
	}

	return set
}
