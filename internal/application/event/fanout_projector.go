// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/adapters/messaging"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

const fanoutHistoryPreamble = "You were added to this conversation. Here is the thread so far:\n\n"

type fanoutReader interface {
	ConversationByID(ctx context.Context, id uuid.UUID) (model.Conversation, error)
	ParticipantsByConversation(ctx context.Context, conversationID uuid.UUID) ([]model.ConversationParticipant, error)
	MessagesByConversation(ctx context.Context, conversationID uuid.UUID) ([]model.Message, error)
	ActiveCandidatesByConversations(ctx context.Context, conversationIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error)
}

type fanoutProviderResolver interface {
	ForMember(ctx context.Context, projectID, memberID uuid.UUID) (service.Provider, error)
}

type fanoutAttachmentReader interface {
	ByConversation(ctx context.Context, conversationID uuid.UUID) ([]model.Attachment, error)
}

type fanoutSigner interface {
	SignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	Enabled() bool
}

const fanoutAttachmentTTL = 30 * time.Minute

// FanoutProjector relays thread messages and participant history.
func FanoutProjector(
	reader fanoutReader,
	providers fanoutProviderResolver,
	members memberByIDReader,
	surfaces *SurfaceManager,
	attachments fanoutAttachmentReader,
	blobs fanoutSigner,
	logger *slog.Logger,
) message.NoPublishHandlerFunc {
	return func(msg *message.Message) error {
		env, err := messaging.DecodeEnvelope(msg.Payload)
		if err != nil {
			return err
		}

		ctx := msg.Context()

		eventType := env.GetType()

		if eventType == orakov1.EventType_EVENT_TYPE_MESSAGE_POSTED {
			fanoutMessage(ctx, reader, providers, members, surfaces, attachments, blobs, env.GetMessagePosted(), logger)
		}

		if eventType == orakov1.EventType_EVENT_TYPE_CONVERSATION_PARTICIPANT_ADDED {
			fanoutHistoryToNewParticipant(ctx, reader, providers, surfaces, env.GetConversationParticipantAdded(), logger)
		}

		return nil
	}
}

func fanoutMessage(
	ctx context.Context,
	reader fanoutReader,
	providers fanoutProviderResolver,
	members memberByIDReader,
	surfaces *SurfaceManager,
	attachments fanoutAttachmentReader,
	blobs fanoutSigner,
	mp *orakov1.MessagePosted,
	logger *slog.Logger,
) {
	if mp == nil {
		return
	}

	role := mp.GetRole()
	if role == orakov1.MessageRole_MESSAGE_ROLE_SYSTEM || role == orakov1.MessageRole_MESSAGE_ROLE_QUESTION {
		return
	}

	convID, err := uuid.Parse(mp.GetConversationId())
	if err != nil {
		logger.WarnContext(ctx, "fanout: malformed conversation id", slog.String("value", mp.GetConversationId()))

		return
	}

	msgID, _ := uuid.Parse(mp.GetMessageId())
	authorID, _ := uuid.Parse(mp.GetAuthorMemberId())

	conv, err := reader.ConversationByID(ctx, convID)
	if err != nil {
		logger.WarnContext(ctx, "fanout: resolving conversation", slog.String("conversation_id", convID.String()), slog.Any("error", err))

		return
	}

	added, err := reader.ParticipantsByConversation(ctx, convID)
	if err != nil {
		logger.WarnContext(ctx, "fanout: resolving participants", slog.String("conversation_id", convID.String()), slog.Any("error", err))

		return
	}

	candidates, err := reader.ActiveCandidatesByConversations(ctx, []uuid.UUID{convID})
	if err != nil {
		logger.WarnContext(ctx, "fanout: resolving candidates", slog.String("conversation_id", convID.String()), slog.Any("error", err))

		return
	}

	// Agent-authored messages still notify the human behind the agent.
	excludeAuthor := authorID
	if mp.GetSource() == string(model.MessageSourceAgent) {
		excludeAuthor = uuid.Nil
	}

	recipients := fanoutRecipients(conv, added, candidates[convID], excludeAuthor)

	authorName := service.ResolveDisplayName(ctx, members, authorID)
	label := mirrorLabel(ctx, members, authorID, authorName, mp.GetOriginSurface(), mp.GetSource())

	outAtts := resolveOutboundAttachments(ctx, attachments, blobs, convID, msgID, logger)

	var covered map[uuid.UUID]bool

	if surface, ok := surfaces.SurfaceFor(ctx, convID); ok {
		covered = coveredSet(surface)

		if mp.GetOriginSurface() != surface.Origin() {
			surfaces.PostToSurface(ctx, conv.ProjectID, surface, label, mp.GetBody(), outAtts)
		}
	}

	for _, memberID := range recipients {
		if covered[memberID] {
			continue
		}

		deliverFanoutRelay(ctx, providers, conv.ProjectID, convID, memberID, label, mp.GetBody(), outAtts, logger)
	}
}

func resolveOutboundAttachments(ctx context.Context, attachments fanoutAttachmentReader, blobs fanoutSigner, conversationID, messageID uuid.UUID, logger *slog.Logger) []service.OutboundAttachment {
	if attachments == nil || blobs == nil || !blobs.Enabled() || messageID == uuid.Nil {
		return nil
	}

	atts, err := attachments.ByConversation(ctx, conversationID)
	if err != nil {
		logger.WarnContext(ctx, "fanout: resolving attachments", slog.String("conversation_id", conversationID.String()), slog.Any("error", err))

		return nil
	}

	var out []service.OutboundAttachment

	for _, a := range atts {
		if a.MessageID != messageID {
			continue
		}

		url, err := blobs.SignedGetURL(ctx, a.StorageKey, fanoutAttachmentTTL)
		if err != nil {
			logger.WarnContext(ctx, "fanout: signing attachment url", slog.String("attachment_id", a.ID.String()), slog.Any("error", err))

			continue
		}

		out = append(out, service.OutboundAttachment{
			Filename:  a.Filename,
			MimeType:  a.MimeType,
			SizeBytes: a.SizeBytes,
			URL:       url,
		})
	}

	return out
}

func signConversationAttachments(ctx context.Context, attachments fanoutAttachmentReader, blobs fanoutSigner, conversationID uuid.UUID, logger *slog.Logger) []service.OutboundAttachment {
	if attachments == nil || blobs == nil || !blobs.Enabled() {
		return nil
	}

	atts, err := attachments.ByConversation(ctx, conversationID)
	if err != nil {
		logger.WarnContext(ctx, "attachments: resolving for open-time delivery", slog.String("conversation_id", conversationID.String()), slog.Any("error", err))

		return nil
	}

	var out []service.OutboundAttachment

	for _, a := range atts {
		if a.MessageID == uuid.Nil {
			continue
		}

		url, err := blobs.SignedGetURL(ctx, a.StorageKey, fanoutAttachmentTTL)
		if err != nil {
			logger.WarnContext(ctx, "attachments: signing open-time url", slog.String("attachment_id", a.ID.String()), slog.Any("error", err))

			continue
		}

		out = append(out, service.OutboundAttachment{
			Filename:  a.Filename,
			MimeType:  a.MimeType,
			SizeBytes: a.SizeBytes,
			URL:       url,
		})
	}

	return out
}

func mirrorLabel(ctx context.Context, members memberByIDReader, authorID uuid.UUID, authorName, originSurface, source string) string {
	if source == string(model.MessageSourceAgent) {
		return authorName + " · Agent"
	}

	platform := ""

	if i := strings.Index(originSurface, ":"); i > 0 {
		platform = originSurface[:i]
	} else if member, err := members.ByID(ctx, authorID); err == nil {
		if ch := member.DeliveryChannel; ch != "" && ch != model.DeliveryChannelDashboard {
			platform = string(ch)
		}
	}

	if platform == "" {
		return authorName
	}

	return authorName + " · " + strings.ToUpper(platform[:1]) + platform[1:]
}

func fanoutHistoryToNewParticipant(
	ctx context.Context,
	reader fanoutReader,
	providers fanoutProviderResolver,
	surfaces *SurfaceManager,
	pa *orakov1.ConversationParticipantAdded,
	logger *slog.Logger,
) {
	if pa == nil {
		return
	}

	convID, err := uuid.Parse(pa.GetConversationId())
	if err != nil {
		logger.WarnContext(ctx, "fanout: malformed conversation id on participant_added", slog.String("value", pa.GetConversationId()))

		return
	}

	memberID, err := uuid.Parse(pa.GetMemberId())
	if err != nil {
		logger.WarnContext(ctx, "fanout: malformed member id on participant_added", slog.String("value", pa.GetMemberId()))

		return
	}

	if surfaces.CoverParticipant(ctx, convID, memberID) {
		return
	}

	conv, err := reader.ConversationByID(ctx, convID)
	if err != nil {
		logger.WarnContext(ctx, "fanout: resolving conversation for history", slog.String("conversation_id", convID.String()), slog.Any("error", err))

		return
	}

	msgs, err := reader.MessagesByConversation(ctx, convID)
	if err != nil {
		logger.WarnContext(ctx, "fanout: resolving thread for history", slog.String("conversation_id", convID.String()), slog.Any("error", err))

		return
	}

	history := renderThreadPlainText(msgs)
	if history == "" {
		return
	}

	deliverFanout(ctx, providers, conv.ProjectID, convID, memberID, fanoutHistoryPreamble+history, service.MessageKindHistory, logger)
}

func fanoutRecipients(conv model.Conversation, added []model.ConversationParticipant, candidates []uuid.UUID, author uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{uuid.Nil: true, author: true}

	var out []uuid.UUID

	add := func(id uuid.UUID) {
		if seen[id] {
			return
		}

		seen[id] = true

		out = append(out, id)
	}

	add(conv.AskerMemberID)
	add(conv.ResponderMemberID)

	for _, p := range added {
		add(p.MemberID)
	}

	for _, id := range candidates {
		add(id)
	}

	return out
}

func deliverFanoutRelay(
	ctx context.Context,
	providers fanoutProviderResolver,
	projectID, conversationID, memberID uuid.UUID,
	mirrorAuthor, body string,
	attachments []service.OutboundAttachment,
	logger *slog.Logger,
) {
	prov, err := providers.ForMember(ctx, projectID, memberID)
	if err != nil {
		if !errors.Is(err, service.ErrNoProvider) {
			logger.WarnContext(ctx, "fanout: resolving provider",
				slog.String("member_id", memberID.String()), slog.Any("error", err))
		}

		return
	}

	if _, err := prov.Deliver(ctx, service.OutboundMessage{
		ProjectID:         projectID,
		ConversationID:    conversationID,
		ResponderMemberID: uuid.Nil,
		RecipientMemberID: memberID,
		Kind:              service.MessageKindRelay,
		MirrorAuthor:      mirrorAuthor,
		Question:          body,
		Attachments:       attachments,
	}); err != nil {
		logger.WarnContext(ctx, "fanout: deliver",
			slog.String("member_id", memberID.String()), slog.Any("error", err))
	}
}

func deliverFanout(
	ctx context.Context,
	providers fanoutProviderResolver,
	projectID, conversationID, memberID uuid.UUID,
	body string,
	kind service.MessageKind,
	logger *slog.Logger,
) {
	prov, err := providers.ForMember(ctx, projectID, memberID)
	if err != nil {
		if !errors.Is(err, service.ErrNoProvider) {
			logger.WarnContext(ctx, "fanout: resolving provider",
				slog.String("member_id", memberID.String()), slog.Any("error", err))
		}

		return
	}

	if _, err := prov.Deliver(ctx, service.OutboundMessage{
		ProjectID:         projectID,
		ConversationID:    conversationID,
		ResponderMemberID: uuid.Nil,
		RecipientMemberID: memberID,
		Kind:              kind,
		Question:          body,
	}); err != nil {
		logger.WarnContext(ctx, "fanout: deliver",
			slog.String("member_id", memberID.String()), slog.Any("error", err))
	}
}
