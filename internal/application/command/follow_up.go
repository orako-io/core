// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/errs"
)

type candidacyStore interface {
	IsActiveCandidate(ctx context.Context, conversationID, memberID uuid.UUID) (bool, error)
	RecordFirstResponder(ctx context.Context, conversationID, memberID uuid.UUID) (bool, error)
}

// FollowUpOutcome describes the result of a follow-up.
type FollowUpOutcome int

// Follow-up outcomes.
const (
	OutcomeAppended FollowUpOutcome = iota
)

// FollowUpResult is Handle's success value.
type FollowUpResult struct {
	Outcome FollowUpOutcome
}

// FollowUpCommand appends a message to an open conversation.
type FollowUpCommand struct {
	ConversationID uuid.UUID
	AuthorMemberID uuid.UUID
	Message        string
	Source         model.MessageSource `exhaustruct:"optional"`
	AgentClient    string              `exhaustruct:"optional"`
	OriginSurface  string              `exhaustruct:"optional"`
	AttachmentIDs  []uuid.UUID         `exhaustruct:"optional"`
	Summary        string              `exhaustruct:"optional"`
	Tags           []string            `exhaustruct:"optional"`
	Entities       []string            `exhaustruct:"optional"`
}

type attachmentLinker interface {
	LinkToMessage(ctx context.Context, conversationID, messageID uuid.UUID, attachmentIDs []uuid.UUID) (int64, error)
}

// FollowUpHandler handles FollowUpCommand.
type FollowUpHandler struct {
	convRepo     repository.ConversationRepository
	bus          eventBus
	candidacy    candidacyStore
	participants participantStore
	txor         service.Transactor
	attachments  attachmentLinker
}

// MustNewFollowUpHandler builds a FollowUpHandler.
func MustNewFollowUpHandler(
	convRepo repository.ConversationRepository,
	bus eventBus,
	candidacy candidacyStore,
	participants participantStore,
	txor service.Transactor,
	attachments attachmentLinker,
) FollowUpHandler {
	if convRepo == nil || bus == nil || candidacy == nil || participants == nil || txor == nil {
		panic("MustNewFollowUpHandler: required dependency is nil")
	}

	return FollowUpHandler{
		convRepo:     convRepo,
		bus:          bus,
		candidacy:    candidacy,
		participants: participants,
		txor:         txor,
		attachments:  attachments,
	}
}

// Handle appends a reply and publishes its event.
func (h FollowUpHandler) Handle(ctx context.Context, cmd FollowUpCommand) (FollowUpResult, error) {
	conv, err := h.convRepo.ConversationByID(ctx, cmd.ConversationID)
	if err != nil {
		return FollowUpResult{}, translateErr(err, "conversation")
	}

	if conv.Status != model.ConversationStatusOpen && conv.Status != model.ConversationStatusAnswered {
		return FollowUpResult{}, errs.InvalidError{
			Field:  fieldConversationID,
			Reason: "conversation is closed",
		}
	}

	if err := memberOnConversation(ctx, conv, cmd.AuthorMemberID, h.participants, h.candidacy, "reply to this conversation"); err != nil {
		return FollowUpResult{}, err
	}

	role := model.MessageRoleFollowUp
	if cmd.AuthorMemberID != conv.AskerMemberID {
		role = model.MessageRoleAnswer
	}

	if err := h.txor.WithTx(ctx, func(ctx context.Context) error {
		h.recordFirstResponder(ctx, &conv, cmd, role)

		msgID, err := h.postThreadMessage(ctx, conv, cmd, role)
		if err != nil {
			return err
		}

		if err := linkMessageAttachments(ctx, h.attachments, conv.ID, msgID, cmd.AttachmentIDs); err != nil {
			return err
		}

		if err := h.applyMetadata(ctx, conv, cmd); err != nil {
			return err
		}

		if _, err = h.bus.Publish(ctx, &orakov1.Envelope{
			ProjectId: conv.ProjectID.String(),
			Type:      orakov1.EventType_EVENT_TYPE_MESSAGE_POSTED,
			Payload: &orakov1.Envelope_MessagePosted{
				MessagePosted: &orakov1.MessagePosted{
					ConversationId: cmd.ConversationID.String(),
					MessageId:      msgID.String(),
					AuthorMemberId: cmd.AuthorMemberID.String(),
					Role:           pbMessageRole(role),
					Body:           cmd.Message,
					OriginSurface:  cmd.OriginSurface,
					Source:         string(cmd.Source),
					AttachmentIds:  uuidsToStrings(cmd.AttachmentIDs),
				},
			},
		}); err != nil {
			return errs.InternalError{Err: fmt.Errorf("publishing message_posted(follow_up): %w", err)}
		}

		return nil
	}); err != nil {
		return FollowUpResult{}, err
	}

	return FollowUpResult{Outcome: OutcomeAppended}, nil
}

func (h FollowUpHandler) postThreadMessage(ctx context.Context, conv model.Conversation, cmd FollowUpCommand, role model.MessageRole) (uuid.UUID, error) {
	msgID := uuid.New()

	msg, err := model.NewMessage(msgID, cmd.ConversationID, cmd.AuthorMemberID, role, cmd.Message, cmd.Source)
	if err != nil {
		return uuid.Nil, err
	}

	msg.AgentClient = cmd.AgentClient

	if err = h.convRepo.AddMessage(ctx, msg); err != nil {
		return uuid.Nil, translateErr(err, "message")
	}

	switch {
	case role == model.MessageRoleAnswer && conv.Status == model.ConversationStatusOpen:
		if err = h.convRepo.UpdateStatus(ctx, conv.ID, model.ConversationStatusAnswered); err != nil {
			return uuid.Nil, translateErr(err, "conversation")
		}
	case role == model.MessageRoleFollowUp && conv.Status == model.ConversationStatusAnswered:
		if err = h.convRepo.UpdateStatus(ctx, conv.ID, model.ConversationStatusOpen); err != nil {
			return uuid.Nil, translateErr(err, "conversation")
		}
	}

	return msgID, nil
}

func (h FollowUpHandler) recordFirstResponder(ctx context.Context, conv *model.Conversation, cmd FollowUpCommand, role model.MessageRole) {
	if role != model.MessageRoleAnswer || conv.ResponderMemberID != uuid.Nil {
		return
	}

	won, err := h.candidacy.RecordFirstResponder(ctx, conv.ID, cmd.AuthorMemberID)
	if err != nil {
		slog.WarnContext(ctx, "follow_up: recording first responder",
			slog.String("conversation_id", conv.ID.String()), slog.Any("error", err))

		return
	}

	if won {
		conv.ResponderMemberID = cmd.AuthorMemberID
	}
}

func (h FollowUpHandler) applyMetadata(ctx context.Context, conv model.Conversation, cmd FollowUpCommand) error {
	summary := model.NormalizeSummary(cmd.Summary)
	tags := model.NormalizeTags(cmd.Tags)
	entities := model.NormalizeTags(cmd.Entities)

	if summary == "" && len(tags) == 0 && len(entities) == 0 {
		return nil
	}

	if summary == "" {
		summary = conv.Summary
	}

	if len(tags) == 0 {
		tags = conv.Tags
	}

	if len(entities) == 0 {
		entities = conv.Entities
	}

	if _, err := h.convRepo.UpdateMetadata(ctx, conv.ID, summary, tags, entities); err != nil {
		return translateErr(err, "conversation")
	}

	return nil
}

func pbMessageRole(role model.MessageRole) orakov1.MessageRole {
	if role == model.MessageRoleAnswer {
		return orakov1.MessageRole_MESSAGE_ROLE_ANSWER
	}

	return orakov1.MessageRole_MESSAGE_ROLE_FOLLOW_UP
}

func linkMessageAttachments(ctx context.Context, linker attachmentLinker, conversationID, messageID uuid.UUID, ids []uuid.UUID) error {
	if linker == nil || len(ids) == 0 {
		return nil
	}

	n, err := linker.LinkToMessage(ctx, conversationID, messageID, ids)
	if err != nil {
		return errs.InternalError{Err: fmt.Errorf("linking attachments: %w", err)}
	}

	if int(n) != len(ids) {
		slog.WarnContext(ctx, "some attachment ids did not link (wrong conversation or already linked)",
			slog.Int("requested", len(ids)), slog.Int64("linked", n))
	}

	return nil
}

func uuidsToStrings(ids []uuid.UUID) []string {
	if len(ids) == 0 {
		return nil
	}

	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}

	return out
}
