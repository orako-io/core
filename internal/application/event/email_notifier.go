// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"context"
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

// memberByIDReader is the narrow read port the email notifier needs to resolve a
// responder's address and delivery channel. *identity.MemberStore satisfies it.
type memberByIDReader interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Member, error)
}

// EmailNotifier is the async subscriber that emails a dashboard-channel
// responder when a question is opened for them. It is the notification that
// keeps the dashboard channel from being a black hole: external channels
// (Slack/Telegram) already ping the responder in-app, so only the dashboard
// channel needs an email nudge — the handler filters on that.
//
// It reacts to CONVERSATION_OPENED off the event stream rather than sending
// inline in Ask, so a slow or failing SMTP server never blocks the
// ask; a transient failure returns an error and the router retries.
//
// Pool dispatch (no assigned responder yet) is NOT handled here: the
// delivery notifier owns the pool fan-out decision (external-channel
// candidates get a provider DM, dashboard/unbound candidates get emailed) so
// a pool candidate is never nudged from two places at once. See
// delivery_notifier.go.
func EmailNotifier(members memberByIDReader, mailer service.Mailer, baseURL string, logger *slog.Logger) message.NoPublishHandlerFunc {
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

		// Pool dispatch: handled exclusively by the delivery notifier.
		if opened.GetMemberId() == "" {
			return nil
		}

		targetID, err := uuid.Parse(opened.GetMemberId())
		if err != nil {
			// Malformed id is a poison message: returning the error would retry
			// it forever. Log and drop instead.
			logger.WarnContext(msg.Context(), "email notifier: malformed responder id",
				slog.String("value", opened.GetMemberId()), slog.Any("error", err))

			return nil
		}

		return nudgeMember(msg.Context(), members, mailer, targetID, opened, baseURL, logger)
	}
}

// nudgeMember emails one dashboard-channel member about the waiting question.
// Non-dashboard channels are skipped: they already got an in-app/provider
// ping through their own channel.
func nudgeMember(ctx context.Context, members memberByIDReader, mailer service.Mailer, memberID uuid.UUID, opened *orakov1.ConversationOpened, baseURL string, logger *slog.Logger) error {
	member, err := members.ByID(ctx, memberID)
	if err != nil {
		logger.WarnContext(ctx, "email notifier: cannot resolve member",
			slog.String("member_id", memberID.String()), slog.Any("error", err))

		return nil
	}

	// Only the dashboard channel gets an email nudge. Empty defaults to
	// dashboard (a member row predating the delivery_channel column).
	if member.DeliveryChannel != model.DeliveryChannelDashboard && member.DeliveryChannel != "" {
		return nil
	}

	return sendQuestionEmail(ctx, mailer, member, opened, baseURL)
}

// sendQuestionEmail unconditionally emails member about the waiting question
// (no channel filter — callers decide when that applies). A missing address
// (e.g. after a PII purge) is a silent no-op: there is nowhere to send.
func sendQuestionEmail(ctx context.Context, mailer service.Mailer, member model.Member, opened *orakov1.ConversationOpened, baseURL string) error {
	if member.Email == "" {
		return nil
	}

	if err := mailer.Send(ctx, questionWaitingEmail(member, opened, baseURL)); err != nil {
		// Transient transport failure — return so the router retries.
		return fmt.Errorf("email notifier: %w", err)
	}

	return nil
}

// questionWaitingEmail builds the "you have a question" email for a dashboard
// responder.
//
// NOTE: this copy is a placeholder. The final wording is a designer
// deliverable (missing-part-designer.md §B, "tu as une question en attente")
// that is not in the current mockup; wire the real template when it lands.
func questionWaitingEmail(member model.Member, opened *orakov1.ConversationOpened, baseURL string) model.EmailMessage {
	inboxURL := strings.TrimRight(baseURL, "/") + "/inbox"

	var text strings.Builder

	fmt.Fprintf(&text, "Hi %s,\n\n", displayNameOrThere(member))
	text.WriteString("You have a new question waiting in your Orako inbox:\n\n")
	fmt.Fprintf(&text, "  %q\n\n", opened.GetQuestion())
	fmt.Fprintf(&text, "Open your inbox to answer: %s\n\n", inboxURL)
	text.WriteString("— Orako\n")

	return model.EmailMessage{
		To:       member.Email,
		Subject:  "You have a question waiting in Orako",
		TextBody: text.String(),
	}
}

// displayNameOrThere returns the member's display name, or "there" as a neutral
// fallback so the greeting reads naturally when the name is unknown.
func displayNameOrThere(member model.Member) string {
	if strings.TrimSpace(member.DisplayName) == "" {
		return "there"
	}

	return member.DisplayName
}
