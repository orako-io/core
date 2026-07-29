// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/errs"
)

const waitWindow = 90 * time.Second

const waitPollInterval = time.Second

// AskCommand opens a direct or domain-routed conversation.
type AskCommand struct {
	ProjectID         uuid.UUID
	AskerMemberID     uuid.UUID
	ResponderMemberID uuid.UUID `exhaustruct:"optional"`

	Domains       []string `exhaustruct:"optional"`
	Question      string
	Title         string `exhaustruct:"optional"`
	Context       string
	Summary       string   `exhaustruct:"optional"`
	Tags          []string `exhaustruct:"optional"`
	Entities      []string `exhaustruct:"optional"`
	Wait          bool
	AgentClient   string      `exhaustruct:"optional"`
	AttachmentIDs []uuid.UUID `exhaustruct:"optional"`
}

type candidatePoolResolver interface {
	MembersByDomains(ctx context.Context, projectID uuid.UUID, domains []string, exclude uuid.UUID) ([]uuid.UUID, error)
}

type memberBindingReader interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Member, error)
}

type projectReader interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Project, error)
}

// AskHandler handles AskCommand.
type AskHandler struct {
	convOpener     conversationOpener
	convRepo       repository.ConversationRepository // read path (poll during wait)
	bus            eventBus
	providerLookup providerLookup
	candidates     candidatePoolResolver
	members        memberBindingReader
	projects       projectReader
	txor           service.Transactor
	attachments    attachmentLinker `exhaustruct:"optional"`
}

// MustNewAskHandler builds an AskHandler.
func MustNewAskHandler(
	convOpener conversationOpener,
	convRepo repository.ConversationRepository,
	bus eventBus,
	providerLookup providerLookup,
	candidates candidatePoolResolver,
	members memberBindingReader,
	projects projectReader,
	txor service.Transactor,
	attachments attachmentLinker,
) AskHandler {
	if candidates == nil || convOpener == nil || convRepo == nil || bus == nil ||
		providerLookup == nil || members == nil || projects == nil || txor == nil {
		panic("MustNewAskHandler: required dependency is nil")
	}

	return AskHandler{
		convOpener:     convOpener,
		convRepo:       convRepo,
		bus:            bus,
		providerLookup: providerLookup,
		candidates:     candidates,
		members:        members,
		projects:       projects,
		txor:           txor,
		attachments:    attachments,
	}
}

// Handle opens the conversation and optionally waits for an inline answer.
func (h AskHandler) Handle(ctx context.Context, cmd AskCommand) (AskResult, error) {
	pool := len(cmd.Domains) > 0
	if pool == (cmd.ResponderMemberID != uuid.Nil) {
		return AskResult{}, errs.InvalidError{
			Field:  fieldMemberID,
			Reason: "provide exactly one of member_id (direct ask) or domains (pool dispatch)",
		}
	}

	if err := h.ensureDirectTargetRoutable(ctx, cmd, pool); err != nil {
		return AskResult{}, err
	}

	_, candidates, err := h.resolveTarget(ctx, cmd, pool)
	if err != nil {
		return AskResult{}, err
	}

	conv, err := buildAskConversation(cmd)
	if err != nil {
		return AskResult{}, err
	}

	convID := conv.ID
	msgID := uuid.New()

	msg, err := model.NewMessage(msgID, convID, cmd.AskerMemberID, model.MessageRoleQuestion, cmd.Question, model.MessageSourceAgent)
	if err != nil {
		return AskResult{}, err
	}

	msg.AgentClient = cmd.AgentClient

	if err = h.persistAsk(ctx, cmd, conv, msg, candidates, pool); err != nil {
		return AskResult{}, err
	}

	result := AskResult{ConversationID: convID, PoolSize: len(candidates)}

	if cmd.Wait {
		result = h.pollForAnswer(ctx, convID, result)
	}

	return h.withNames(ctx, cmd, candidates, pool, result), nil
}

func (h AskHandler) persistAsk(
	ctx context.Context,
	cmd AskCommand,
	conv model.Conversation,
	msg model.Message,
	candidates []uuid.UUID,
	pool bool,
) error {
	return h.txor.WithTx(ctx, func(ctx context.Context) error {
		if err := h.convOpener.OpenConversation(ctx, conv, msg, candidates); err != nil {
			return translateErr(err, "conversation")
		}

		if err := linkMessageAttachments(ctx, h.attachments, conv.ID, msg.ID, cmd.AttachmentIDs); err != nil {
			return err
		}

		specialistWire := ""
		if !pool {
			specialistWire = cmd.ResponderMemberID.String()
		}

		if _, err := h.bus.Publish(ctx, &orakov1.Envelope{
			ProjectId: cmd.ProjectID.String(),
			Type:      orakov1.EventType_EVENT_TYPE_CONVERSATION_OPENED,
			Payload: &orakov1.Envelope_ConversationOpened{
				ConversationOpened: &orakov1.ConversationOpened{
					ConversationId: conv.ID.String(),
					ProjectId:      cmd.ProjectID.String(),
					AskerMemberId:  cmd.AskerMemberID.String(),
					MemberId:       specialistWire,
					Question:       cmd.Question,
					Context:        cmd.Context,
				},
			},
		}); err != nil {
			return errs.InternalError{Err: fmt.Errorf("publishing conversation_opened: %w", err)}
		}

		if _, err := h.bus.Publish(ctx, &orakov1.Envelope{
			ProjectId: cmd.ProjectID.String(),
			Type:      orakov1.EventType_EVENT_TYPE_MESSAGE_POSTED,
			Payload: &orakov1.Envelope_MessagePosted{
				MessagePosted: &orakov1.MessagePosted{
					ConversationId: conv.ID.String(),
					MessageId:      msg.ID.String(),
					AuthorMemberId: cmd.AskerMemberID.String(),
					Source:         string(model.MessageSourceAgent),
					Role:           orakov1.MessageRole_MESSAGE_ROLE_QUESTION,
					Body:           cmd.Question,
					AttachmentIds:  uuidsToStrings(cmd.AttachmentIDs),
				},
			},
		}); err != nil {
			return errs.InternalError{Err: fmt.Errorf("publishing message_posted: %w", err)}
		}

		return nil
	})
}

func buildAskConversation(cmd AskCommand) (model.Conversation, error) {
	var zero model.Conversation

	summary, tags, entities, err := normalizeRequiredMetadata(cmd.Summary, cmd.Tags, cmd.Entities)
	if err != nil {
		return zero, err
	}

	if err := model.ValidateContextLen(cmd.Context); err != nil {
		return zero, err
	}

	conv, err := model.NewConversation(uuid.New(), cmd.ProjectID, cmd.AskerMemberID, cmd.Question)
	if err != nil {
		return zero, err
	}

	conv.ResponderMemberID = cmd.ResponderMemberID
	conv.Title = strings.TrimSpace(cmd.Title)
	conv.Context = cmd.Context
	conv.Summary = summary
	conv.Tags = tags
	conv.Entities = entities

	return conv, nil
}

func (h AskHandler) withNames(
	ctx context.Context,
	cmd AskCommand,
	candidates []uuid.UUID,
	pool bool,
	result AskResult,
) AskResult {
	targetIDs := candidates
	if !pool {
		targetIDs = []uuid.UUID{cmd.ResponderMemberID}
	}

	result.ProjectName = h.resolveProjectName(ctx, cmd.ProjectID)
	result.RecipientNames = h.resolveRecipientNames(ctx, targetIDs)

	return result
}

func (h AskHandler) resolveProjectName(ctx context.Context, projectID uuid.UUID) string {
	project, err := h.projects.ByID(ctx, projectID)
	if err != nil {
		slog.Debug("ask: project name lookup failed", "project_id", projectID, "err", err)

		return ""
	}

	return project.Name
}

func (h AskHandler) resolveRecipientNames(ctx context.Context, ids []uuid.UUID) []string {
	var names []string

	for _, id := range ids {
		member, err := h.members.ByID(ctx, id)
		if err != nil {
			slog.Debug("ask: responder name lookup failed", "member_id", id, "err", err)

			continue
		}

		names = append(names, displayLabel(member))
	}

	return names
}

func displayLabel(m model.Member) string {
	if n := strings.TrimSpace(m.DisplayName); n != "" {
		return n
	}

	if n := strings.TrimSpace(m.FirstName + " " + m.LastName); n != "" {
		return n
	}

	if e := strings.TrimSpace(m.Email); e != "" {
		return e
	}

	return m.ID.String()[:8]
}

func (h AskHandler) ensureDirectTargetRoutable(ctx context.Context, cmd AskCommand, pool bool) error {
	if pool {
		return nil
	}

	target, err := h.members.ByID(ctx, cmd.ResponderMemberID)
	if err != nil {
		return translateErr(err, "specialist")
	}

	if target.Status.Routable() {
		return nil
	}

	return errs.InvalidError{Field: "member_id", Reason: unroutableReason(target)}
}

func unroutableReason(m model.Member) string {
	const reroute = " — call list_experts to pick someone else, or pass domains to dispatch to whoever is available."

	if m.Status == model.MemberStatusOnLeave {
		if m.ReturnDate != nil {
			return "this responder is on leave until " + m.ReturnDate.Format("2006-01-02") + reroute
		}

		return "this responder is on leave" + reroute
	}

	if m.Status == model.MemberStatusDeactivated {
		return "this responder has been deactivated" + reroute
	}

	return "this responder is not active (" + string(m.Status) + ")" + reroute
}

func (h AskHandler) resolveTarget(ctx context.Context, cmd AskCommand, pool bool) (service.Provider, []uuid.UUID, error) {
	if pool {
		candidates, err := h.candidates.MembersByDomains(ctx, cmd.ProjectID, cmd.Domains, cmd.AskerMemberID)
		if err != nil {
			return nil, nil, errs.InternalError{Err: fmt.Errorf("resolving candidates: %w", err)}
		}

		if len(candidates) == 0 {
			return nil, nil, errs.InvalidError{
				Field:  "domains",
				Reason: "no active responder matches these domains — check list_experts for available expertise",
			}
		}

		return nil, candidates, nil
	}

	prov, err := h.providerLookup.ForMember(ctx, cmd.ProjectID, cmd.ResponderMemberID)
	if err != nil {
		if errors.Is(err, service.ErrNoProvider) {
			return nil, nil, errs.InvalidError{
				Field:  fieldMemberID,
				Reason: "the responder's messaging channel has no provider configured — configure one with ConfigureProvider, or the responder can switch to the dashboard channel",
			}
		}

		return nil, nil, errs.InternalError{Err: fmt.Errorf("looking up provider: %w", err)}
	}

	return prov, nil, nil
}

func (h AskHandler) pollForAnswer(ctx context.Context, convID uuid.UUID, async AskResult) AskResult {
	deadline := time.Now().Add(waitWindow)

	for {
		msgs, err := h.convRepo.MessagesByConversation(ctx, convID)
		if err == nil {
			for _, m := range msgs {
				if m.Role == model.MessageRoleAnswer {
					return AskResult{
						ConversationID: convID,
						InlineAnswer:   m.Body,
						Answered:       true,
					}
				}
			}
		}

		if time.Now().After(deadline) {
			return async
		}

		select {
		case <-ctx.Done():
			return async
		case <-time.After(waitPollInterval):
		}
	}
}
