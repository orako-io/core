// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/errs"
)

// ResolveConversationCommand is the input for resolving a conversation: it
// freezes the conversation's status and its final curated metadata, and lands
// the agent-supplied resolution as the thread's answer message. The server does
// NOT summarize or generate the resolution — invariant (a): the agent supplies
// the distilled answer and Orako stores it verbatim.
type ResolveConversationCommand struct {
	ConversationID uuid.UUID
	CloserMemberID uuid.UUID
	// Resolution is stored verbatim; it must never be rewritten server-side.
	Resolution string
	// Summary is the agent-curated FINAL one-line gist persisted onto the
	// conversation on resolve. Required (after normalization) — an empty summary
	// is rejected — so the resolved conversation carries the curated metadata
	// history search relies on.
	Summary string `exhaustruct:"optional"`
	// Tags are agent-curated lowercase topic/area labels. At least one is
	// required (after normalization); they are persisted as the conversation's
	// final tags.
	Tags []string `exhaustruct:"optional"`
	// Entities are agent-curated concrete systems/modules the conversation is
	// about; optional, normalized like tags, persisted onto the conversation.
	Entities []string `exhaustruct:"optional"`
	// Contested / ContestedSet are accepted from the wire for compatibility but
	// no longer persisted: the retired KB entry was their only sink.
	Contested    bool `exhaustruct:"optional"`
	ContestedSet bool `exhaustruct:"optional"`
	// Source records how the resolution message was authored — agent (closed via
	// the MCP resolve_conversation tool) or human (dashboard). Empty defaults to
	// human.
	Source model.MessageSource `exhaustruct:"optional"`
	// AgentClient is the MCP client that authored the resolution (e.g.
	// "claude-code") when Source is agent; empty otherwise.
	AgentClient string `exhaustruct:"optional"`
}

// ResolveConversationResult carries the IDs created by the command. It is empty
// now that resolve no longer distils a KB entry; it is retained as the handler's
// return shape so future resolve-time outputs have a home.
type ResolveConversationResult struct{}

// ResolveConversationHandler handles ResolveConversationCommand.
type ResolveConversationHandler struct {
	convRepo repository.ConversationRepository // ConversationByID + UpdateStatus + UpdateMetadata + AddMessage
	txor     service.Transactor                // composes the status + metadata + answer writes in one transaction
	bus      eventBus
}

// MustNewResolveConversationHandler builds a handler and panics only on invalid
// composition-root wiring.
func MustNewResolveConversationHandler(
	convRepo repository.ConversationRepository,
	txor service.Transactor,
	bus eventBus,
) ResolveConversationHandler {
	if convRepo == nil || txor == nil || bus == nil {
		panic("MustNewResolveConversationHandler: required dependency is nil")
	}

	return ResolveConversationHandler{
		convRepo: convRepo,
		txor:     txor,
		bus:      bus,
	}
}

// resolve runs the resolution as one transaction: mark the conversation
// resolved, persist the final curated metadata, and land the answer message on
// the thread. Each repository joins the ambient transaction through its context,
// so the writes commit or roll back together.
func (h ResolveConversationHandler) resolve(
	ctx context.Context,
	convID uuid.UUID,
	answer *model.Message,
	summary string,
	tags []string,
	entities []string,
) error {
	return h.txor.WithTx(ctx, func(ctx context.Context) error {
		if err := h.convRepo.UpdateStatus(ctx, convID, model.ConversationStatusResolved); err != nil {
			return err
		}

		if _, err := h.convRepo.UpdateMetadata(ctx, convID, summary, tags, entities); err != nil {
			return err
		}

		if answer != nil {
			return h.convRepo.AddMessage(ctx, *answer)
		}

		return nil
	})
}

// Handle resolves the conversation: it freezes the status and final metadata and
// lands the agent-supplied resolution as the thread's answer message, in one
// transaction (composed via the service.Transactor), including the durable
// outbox events. No server-generated text is ever written.
func (h ResolveConversationHandler) Handle(ctx context.Context, cmd ResolveConversationCommand) (ResolveConversationResult, error) {
	conv, err := h.convRepo.ConversationByID(ctx, cmd.ConversationID)
	if err != nil {
		return ResolveConversationResult{}, translateErr(err, "conversation")
	}

	if conv.Status == model.ConversationStatusResolved {
		return ResolveConversationResult{}, errs.InvalidError{
			Field:  fieldConversationID,
			Reason: "conversation is already resolved",
		}
	}

	// Required-but-guided FINAL metadata: the resolved conversation must carry a
	// curated summary and at least one tag — these are what history search reads.
	// A resolve that supplies no metadata (the dashboard/Connect-RPC path)
	// inherits the conversation's CURRENT curated summary/tags/entities set at
	// ask/follow_up time; only a resolve where both supplied AND existing are
	// empty is rejected.
	summary, tags, entities, err := resolveMetadata(
		cmd.Summary, cmd.Tags, cmd.Entities,
		conv.Summary, conv.Tags, conv.Entities,
	)
	if err != nil {
		return ResolveConversationResult{}, err
	}

	answer, err := h.buildAnswerMessage(cmd)
	if err != nil {
		return ResolveConversationResult{}, err
	}

	if err = h.txor.WithTx(ctx, func(ctx context.Context) error {
		if err := h.resolve(ctx, cmd.ConversationID, answer, summary, tags, entities); err != nil {
			return translateErr(err, "conversation")
		}

		if err := h.publishAnswerPosted(ctx, cmd, conv.ProjectID, answer); err != nil {
			return err
		}

		return h.publishClosed(ctx, cmd, conv)
	}); err != nil {
		return ResolveConversationResult{}, err
	}

	return ResolveConversationResult{}, nil
}

// buildAnswerMessage turns the agent-supplied resolution into the thread's
// answer message. Without it the asking agent would see a resolved-empty
// conversation, and the inline wait (which polls for an answer-role message)
// could never resolve. An empty resolution yields a nil message (no-op).
func (h ResolveConversationHandler) buildAnswerMessage(cmd ResolveConversationCommand) (*model.Message, error) {
	if cmd.Resolution == "" {
		return nil, nil
	}

	msg, err := model.NewMessage(uuid.New(), cmd.ConversationID, cmd.CloserMemberID, model.MessageRoleAnswer, cmd.Resolution, cmd.Source)
	if err != nil {
		return nil, err
	}

	msg.AgentClient = cmd.AgentClient

	return &msg, nil
}

// publishAnswerPosted publishes MESSAGE_POSTED for the resolution-as-answer
// message; a nil answer (empty resolution) is a no-op.
func (h ResolveConversationHandler) publishAnswerPosted(ctx context.Context, cmd ResolveConversationCommand, projectID uuid.UUID, answer *model.Message) error {
	if answer == nil {
		return nil
	}

	if _, err := h.bus.Publish(ctx, &orakov1.Envelope{
		ProjectId: projectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_MESSAGE_POSTED,
		Payload: &orakov1.Envelope_MessagePosted{
			MessagePosted: &orakov1.MessagePosted{
				ConversationId: cmd.ConversationID.String(),
				MessageId:      answer.ID.String(),
				AuthorMemberId: cmd.CloserMemberID.String(),
				Role:           orakov1.MessageRole_MESSAGE_ROLE_ANSWER,
				Body:           answer.Body,
			},
		},
	}); err != nil {
		return errs.InternalError{Err: fmt.Errorf("publishing message_posted(answer): %w", err)}
	}

	return nil
}

// publishClosed publishes CONVERSATION_CLOSED for a completed resolution so the
// closure/bridge consumers can notify participants. The KbEntryId field is left
// empty now that resolve no longer distils a KB entry.
func (h ResolveConversationHandler) publishClosed(ctx context.Context, cmd ResolveConversationCommand, conv model.Conversation) error {
	if _, err := h.bus.Publish(ctx, &orakov1.Envelope{
		ProjectId: conv.ProjectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_CONVERSATION_CLOSED,
		Payload: &orakov1.Envelope_ConversationClosed{
			ConversationClosed: &orakov1.ConversationClosed{
				ConversationId:    cmd.ConversationID.String(),
				Resolution:        cmd.Resolution,
				CloserMemberId:    cmd.CloserMemberID.String(),
				ResponderMemberId: conv.ResponderMemberID.String(),
			},
		},
	}); err != nil {
		return errs.InternalError{Err: fmt.Errorf("publishing conversation_closed: %w", err)}
	}

	return nil
}
