// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/adapters/messaging"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

type providerMessageLedger interface {
	ByConversation(ctx context.Context, conversationID uuid.UUID) ([]model.ProviderMessage, error)
	SetState(ctx context.Context, id uuid.UUID, state model.ProviderMessageState) error
}

type bridgeProviderResolver interface {
	ForMember(ctx context.Context, projectID, memberID uuid.UUID) (service.Provider, error)
}

// BridgeProjector projects conversation closure to delivered messages.
func BridgeProjector(
	ledger providerMessageLedger,
	providers bridgeProviderResolver,
	members memberByIDReader,
	surfaces *SurfaceManager,
	logger *slog.Logger,
) message.NoPublishHandlerFunc {
	return func(msg *message.Message) error {
		env, err := messaging.DecodeEnvelope(msg.Payload)
		if err != nil {
			return err
		}

		if env.GetType() == orakov1.EventType_EVENT_TYPE_CONVERSATION_CLOSED {
			return projectClosed(msg.Context(), ledger, providers, members, surfaces, env, logger)
		}

		return nil
	}
}

func projectClosed(
	ctx context.Context,
	ledger providerMessageLedger,
	providers bridgeProviderResolver,
	members memberByIDReader,
	surfaces *SurfaceManager,
	env *orakov1.Envelope,
	logger *slog.Logger,
) error {
	closed := env.GetConversationClosed()
	if closed == nil || closed.GetResolution() == "" {
		return nil // closed without an answer: nothing was resolved to project.
	}

	projectID, err := uuid.Parse(env.GetProjectId())
	if err != nil {
		logger.WarnContext(ctx, "bridge projector: malformed project id",
			slog.String("value", env.GetProjectId()), slog.Any("error", err))

		return nil
	}

	conversationID, err := uuid.Parse(closed.GetConversationId())
	if err != nil {
		logger.WarnContext(ctx, "bridge projector: malformed conversation id",
			slog.String("value", closed.GetConversationId()), slog.Any("error", err))

		return nil
	}

	winnerID, _ := uuid.Parse(closed.GetResponderMemberId())

	rows, err := ledger.ByConversation(ctx, conversationID)
	if err != nil {
		return fmt.Errorf("bridge projector: resolving ledger rows: %w", err)
	}

	winnerName := service.ResolveDisplayName(ctx, members, winnerID)
	content := fmt.Sprintf("✅ Resolved by %s:\n\n%s", winnerName, closed.GetResolution())

	surfaces.CloseSurface(ctx, projectID, conversationID, content)

	var errs []error

	for _, row := range rows {
		if row.MemberID == winnerID {
			continue
		}

		if row.State == model.ProviderMessageStateFailed || row.State == model.ProviderMessageStateResolved {
			continue
		}

		if row.State == model.ProviderMessageStateReserving {
			continue
		}

		if err := projectClosedRow(ctx, providers, ledger, projectID, conversationID, winnerID, row, content, logger); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("bridge projector: projecting closed: %w", errors.Join(errs...))
	}

	return nil
}

func projectClosedRow(
	ctx context.Context,
	providers bridgeProviderResolver,
	ledger providerMessageLedger,
	projectID, conversationID, winnerID uuid.UUID,
	row model.ProviderMessage,
	content string,
	logger *slog.Logger,
) error {
	prov, err := providers.ForMember(ctx, projectID, row.MemberID)
	if err != nil {
		logger.WarnContext(ctx, "bridge projector: resolving provider for closure",
			slog.String("member_id", row.MemberID.String()), slog.Any("error", err))

		return fmt.Errorf("resolving provider for closure, member %s: %w", row.MemberID, err)
	}

	previousState := row.State

	if err := ledger.SetState(ctx, row.ID, model.ProviderMessageStateReserving); err != nil {
		logger.WarnContext(ctx, "bridge projector: reserving row for closure",
			slog.String("provider_message_id", row.ID.String()), slog.Any("error", err))

		return fmt.Errorf("reserving row %s for closure: %w", row.ID, err)
	}

	if _, err := prov.Deliver(ctx, service.OutboundMessage{
		ProjectID:         projectID,
		ConversationID:    conversationID,
		ResponderMemberID: winnerID,
		RecipientMemberID: row.MemberID,
		Kind:              service.MessageKindClosure,
		Question:          content,
		Context:           "",
	}); err != nil {
		logger.WarnContext(ctx, "bridge projector: delivering closure content",
			slog.String("member_id", row.MemberID.String()), slog.Any("error", err))

		if revertErr := ledger.SetState(ctx, row.ID, previousState); revertErr != nil {
			logger.WarnContext(ctx, "bridge projector: reverting failed reservation",
				slog.String("provider_message_id", row.ID.String()), slog.Any("error", revertErr))
		}

		return fmt.Errorf("delivering closure content for member %s: %w", row.MemberID, err)
	}

	if editor, ok := prov.(service.Editor); ok {
		ref := service.MessageRef{ChannelID: row.ChannelID, MessageID: row.MessageRef}
		if err := editor.Edit(ctx, ref, "✅ Resolved — see the message above."); err != nil {
			logger.WarnContext(ctx, "bridge projector: editing DM to resolved",
				slog.String("member_id", row.MemberID.String()), slog.Any("error", err))

			return fmt.Errorf("editing DM to resolved for member %s: %w", row.MemberID, err)
		}
	}

	if err := ledger.SetState(ctx, row.ID, model.ProviderMessageStateResolved); err != nil {
		logger.WarnContext(ctx, "bridge projector: setting resolved state",
			slog.String("provider_message_id", row.ID.String()), slog.Any("error", err))

		return fmt.Errorf("setting resolved state for row %s: %w", row.ID, err)
	}

	return nil
}

func roleLabel(r model.MessageRole) string {
	switch r {
	case model.MessageRoleQuestion:
		return "Question"
	case model.MessageRoleAnswer:
		return "Answer"
	case model.MessageRoleFollowUp:
		return "Follow-up"
	case model.MessageRoleSecondOpinion:
		return "Second opinion"
	case model.MessageRoleSystem, model.MessageRoleUnspecified:
		return "Note"
	default:
		return "Note"
	}
}

func renderThreadPlainText(msgs []model.Message) string {
	var b strings.Builder

	for _, m := range msgs {
		if m.Role == model.MessageRoleSystem {
			continue
		}

		fmt.Fprintf(&b, "%s: %s\n\n", roleLabel(m.Role), m.Body)
	}

	return strings.TrimRight(b.String(), "\n")
}
