// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/adapters/messaging"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

type conversationCandidatesReader interface {
	ByConversation(ctx context.Context, conversationID uuid.UUID) ([]model.Candidate, error)
}

type providerForCandidate interface {
	ForMember(ctx context.Context, projectID, memberID uuid.UUID) (service.Provider, error)
}

type providerMessageLedgerWriter interface {
	Upsert(ctx context.Context, msg model.ProviderMessage) error
	ByConversation(ctx context.Context, conversationID uuid.UUID) ([]model.ProviderMessage, error)
	SetState(ctx context.Context, id uuid.UUID, state model.ProviderMessageState) error
	Finalize(ctx context.Context, id uuid.UUID, channelID, messageRef string, state model.ProviderMessageState) error
}

type memberBindingWriter interface {
	memberByIDReader
	Update(ctx context.Context, member model.Member) error
}

// DeliveryNotifier delivers opened conversations without duplicating replayed events.
func DeliveryNotifier(
	members memberBindingWriter,
	candidates conversationCandidatesReader,
	providers providerForCandidate,
	ledger providerMessageLedgerWriter,
	surfaces *SurfaceManager,
	mailer service.Mailer,
	attachments fanoutAttachmentReader,
	blobs fanoutSigner,
	baseURL string,
	logger *slog.Logger,
) message.NoPublishHandlerFunc {
	return func(msg *message.Message) error {
		env, err := messaging.DecodeEnvelope(msg.Payload)
		if err != nil {
			return err
		}

		if env.GetType() != orakov1.EventType_EVENT_TYPE_CONVERSATION_OPENED {
			return nil
		}

		opened := env.GetConversationOpened()

		if opened == nil {
			return nil
		}

		ctx := msg.Context()

		projectID, convID, ok := parseOpenedIDs(ctx, opened, logger)
		if !ok {
			return nil
		}

		notified, err := alreadyNotifiedSet(ctx, ledger, convID)
		if err != nil {
			return fmt.Errorf("delivery notifier: resolving already-notified members: %w", err)
		}

		outAtts := signConversationAttachments(ctx, attachments, blobs, convID, logger)

		if opened.GetMemberId() != "" {
			return deliverDirect(
				ctx, members, providers, ledger, surfaces, mailer,
				projectID, convID, notified, opened, outAtts, baseURL, logger,
			)
		}

		pool, err := candidates.ByConversation(ctx, convID)
		if err != nil {
			return fmt.Errorf("delivery notifier: resolving candidates: %w", err)
		}

		covered := surfaces.EnsureDiscordThread(ctx, projectID, convID,
			surfaceAudience(opened.GetAskerMemberId(), pool))

		if err := deliverToPool(ctx, members, providers, ledger, mailer, projectID, convID, pool, notified, covered, opened, outAtts, baseURL, logger); err != nil {
			return err
		}

		return nil
	}
}

func deliverDirect(
	ctx context.Context,
	members memberBindingWriter,
	providers providerForCandidate,
	ledger providerMessageLedgerWriter,
	surfaces *SurfaceManager,
	mailer service.Mailer,
	projectID, convID uuid.UUID,
	notified map[uuid.UUID]bool,
	opened *orakov1.ConversationOpened,
	outAtts []service.OutboundAttachment,
	baseURL string,
	logger *slog.Logger,
) error {
	targetID, err := uuid.Parse(opened.GetMemberId())
	if err != nil {
		logger.WarnContext(ctx, "delivery notifier: malformed direct responder id",
			slog.String("value", opened.GetMemberId()))

		return nil //nolint:nilerr // poison event: a malformed persisted UUID cannot become valid on retry
	}

	if targetID == uuid.Nil {
		logger.WarnContext(ctx, "delivery notifier: empty direct responder id")

		return nil
	}

	if notified[targetID] {
		return nil
	}

	covered := surfaces.EnsureDiscordThread(ctx, projectID, convID,
		surfaceAudience(opened.GetAskerMemberId(), []model.Candidate{
			{MemberID: targetID, InvitedAt: time.Time{}, ExcludedAt: nil},
		}))
	if covered[targetID] {
		return nil
	}

	return deliverToCandidate(
		ctx, members, providers, ledger, mailer, projectID, convID, targetID,
		targetID, false, opened, outAtts, baseURL, logger,
	)
}

func deliverToPool(
	ctx context.Context,
	members memberBindingWriter,
	providers providerForCandidate,
	ledger providerMessageLedgerWriter,
	mailer service.Mailer,
	projectID, convID uuid.UUID,
	pool []model.Candidate,
	notified, covered map[uuid.UUID]bool,
	opened *orakov1.ConversationOpened,
	outAtts []service.OutboundAttachment,
	baseURL string,
	logger *slog.Logger,
) error {
	var errs []error

	for _, candidate := range pool {
		if !candidate.Active() {
			continue
		}

		if notified[candidate.MemberID] {
			continue
		}

		if covered[candidate.MemberID] {
			continue
		}

		if err := deliverToCandidate(
			ctx, members, providers, ledger, mailer, projectID, convID,
			candidate.MemberID, uuid.Nil, true, opened, outAtts, baseURL, logger,
		); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("delivery notifier: %w", errors.Join(errs...))
	}

	return nil
}

func surfaceAudience(askerID string, pool []model.Candidate) []uuid.UUID {
	var out []uuid.UUID

	if id, err := uuid.Parse(askerID); err == nil && id != uuid.Nil {
		out = append(out, id)
	}

	for _, c := range pool {
		if c.Active() {
			out = append(out, c.MemberID)
		}
	}

	return out
}

func parseOpenedIDs(ctx context.Context, opened *orakov1.ConversationOpened, logger *slog.Logger) (projectID, conversationID uuid.UUID, ok bool) {
	var err error

	projectID, err = uuid.Parse(opened.GetProjectId())
	if err != nil {
		logger.WarnContext(ctx, "delivery notifier: malformed project id",
			slog.String("value", opened.GetProjectId()), slog.Any("error", err))

		return uuid.Nil, uuid.Nil, false
	}

	conversationID, err = uuid.Parse(opened.GetConversationId())
	if err != nil {
		logger.WarnContext(ctx, "delivery notifier: malformed conversation id",
			slog.String("value", opened.GetConversationId()), slog.Any("error", err))

		return uuid.Nil, uuid.Nil, false
	}

	return projectID, conversationID, true
}

func alreadyNotifiedSet(ctx context.Context, ledger providerMessageLedgerWriter, conversationID uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := ledger.ByConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}

	notified := make(map[uuid.UUID]bool, len(rows))
	for _, row := range rows {
		notified[row.MemberID] = true
	}

	return notified, nil
}

func deliverToCandidate(
	ctx context.Context,
	members memberBindingWriter,
	providers providerForCandidate,
	ledger providerMessageLedgerWriter,
	mailer service.Mailer,
	projectID, conversationID, memberID uuid.UUID,
	responderMemberID uuid.UUID,
	notifyDashboard bool,
	opened *orakov1.ConversationOpened,
	outAtts []service.OutboundAttachment,
	baseURL string,
	logger *slog.Logger,
) error {
	member, err := members.ByID(ctx, memberID)
	if err != nil {
		return fmt.Errorf("resolving candidate %s: %w", memberID, err)
	}

	channel, handled := handleCandidateWithoutProviderBinding(
		ctx,
		mailer,
		member,
		notifyDashboard,
		opened,
		baseURL,
		logger,
	)
	if handled {
		return nil
	}

	prov, handled, err := resolveCandidateProvider(
		ctx,
		providers,
		mailer,
		projectID,
		memberID,
		member,
		opened,
		baseURL,
		logger,
	)
	if err != nil {
		return err
	}

	if handled {
		return nil
	}

	// Reserve before delivery so event retries cannot send a duplicate.
	reservationID := uuid.New()
	if err := ledger.Upsert(ctx, model.ProviderMessage{
		ID:             reservationID,
		ConversationID: conversationID,
		MemberID:       memberID,
		ProviderKind:   string(channel),
		ChannelID:      "",
		MessageRef:     "",
		State:          model.ProviderMessageStateReserving,
	}); err != nil {
		return fmt.Errorf("reserving ledger row for candidate %s: %w", memberID, err)
	}

	ref, err := prov.Deliver(ctx, service.OutboundMessage{
		ProjectID:         projectID,
		ConversationID:    conversationID,
		ResponderMemberID: responderMemberID,
		RecipientMemberID: memberID,
		Kind:              service.MessageKindQuestion,
		Question:          opened.GetQuestion(),
		Context:           opened.GetContext(),
		Attachments:       outAtts,
	})
	if err != nil {
		// Provider errors may contain credential-bearing URLs; never log them raw.
		logger.WarnContext(ctx, "delivery notifier: provider deliver failed; falling back to email",
			slog.String("member_id", memberID.String()), slog.String("channel", string(channel)))
		fallbackAfterFailure(ctx, members, mailer, member, opened, baseURL, logger)

		if setErr := ledger.SetState(ctx, reservationID, model.ProviderMessageStateFailed); setErr != nil {
			logger.WarnContext(ctx, "delivery notifier: marking reservation failed",
				slog.String("member_id", memberID.String()), slog.Any("error", setErr))
		}

		return nil
	}

	if err := ledger.Finalize(ctx, reservationID, ref.ChannelID, ref.MessageID, model.ProviderMessageStatePosted); err != nil {
		return fmt.Errorf("finalizing ledger row for candidate %s: %w", memberID, err)
	}

	return nil
}

func handleCandidateWithoutProviderBinding(
	ctx context.Context,
	mailer service.Mailer,
	member model.Member,
	notifyDashboard bool,
	opened *orakov1.ConversationOpened,
	baseURL string,
	logger *slog.Logger,
) (model.DeliveryChannel, bool) {
	channel := member.DeliveryChannel

	bound := channel != "" &&
		channel != model.DeliveryChannelDashboard &&
		member.BindingFor(channel) != ""
	if bound {
		return channel, false
	}

	if !notifyDashboard && (channel == "" || channel == model.DeliveryChannelDashboard) {
		return channel, true
	}

	if err := sendQuestionEmail(ctx, mailer, member, opened, baseURL); err != nil {
		logger.WarnContext(ctx, "delivery notifier: email nudge failed",
			slog.String("member_id", member.ID.String()), slog.Any("error", err))
	}

	return channel, true
}

func resolveCandidateProvider(
	ctx context.Context,
	providers providerForCandidate,
	mailer service.Mailer,
	projectID, memberID uuid.UUID,
	member model.Member,
	opened *orakov1.ConversationOpened,
	baseURL string,
	logger *slog.Logger,
) (service.Provider, bool, error) {
	prov, err := providers.ForMember(ctx, projectID, memberID)
	if err == nil {
		return prov, false, nil
	}

	if !errors.Is(err, service.ErrNoProvider) {
		return nil, false, fmt.Errorf("resolving provider for candidate %s: %w", memberID, err)
	}

	logger.WarnContext(ctx, "delivery notifier: no provider configured for project; falling back to email",
		slog.String("project_id", projectID.String()), slog.String("member_id", memberID.String()))

	if err := sendQuestionEmail(ctx, mailer, member, opened, baseURL); err != nil {
		logger.WarnContext(ctx, "delivery notifier: email nudge failed",
			slog.String("member_id", memberID.String()), slog.Any("error", err))
	}

	return nil, true, nil
}

func fallbackAfterFailure(
	ctx context.Context,
	members memberBindingWriter,
	mailer service.Mailer,
	member model.Member,
	opened *orakov1.ConversationOpened,
	baseURL string,
	logger *slog.Logger,
) {
	member.BindingError = sanitizedBindingError(member.DeliveryChannel)

	if err := members.Update(ctx, member); err != nil {
		logger.WarnContext(ctx, "delivery notifier: recording binding_error failed",
			slog.String("member_id", member.ID.String()), slog.Any("error", err))
	}

	if err := sendQuestionEmail(ctx, mailer, member, opened, baseURL); err != nil {
		logger.WarnContext(ctx, "delivery notifier: fallback email nudge failed",
			slog.String("member_id", member.ID.String()), slog.Any("error", err))
	}
}

func sanitizedBindingError(channel model.DeliveryChannel) string {
	if channel == "" {
		channel = model.DeliveryChannelDashboard
	}

	return fmt.Sprintf("delivery failed on %s", channel)
}
