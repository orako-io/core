// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"fmt"
	"html/template"
	"log/slog"
	"strings"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/adapters/messaging"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

// ClosureNotifier is the async subscriber that tells a responder how their
// answer was distilled when someone ELSE (the asking agent) closed the
// conversation: the operator must get a feedback loop on what entered the team's
// history in their name — and a chance to correct it from the
// dashboard. A responder closing their own conversation gets nothing (they
// wrote the resolution themselves).
func ClosureNotifier(members memberByIDReader, mailer service.Mailer, baseURL string, logger *slog.Logger) message.NoPublishHandlerFunc {
	return func(msg *message.Message) error {
		env, err := messaging.DecodeEnvelope(msg.Payload)
		if err != nil {
			return err
		}

		if env.GetType() != orakov1.EventType_EVENT_TYPE_CONVERSATION_CLOSED {
			return nil
		}

		closed := env.GetConversationClosed()
		if closed == nil || closed.GetResolution() == "" {
			return nil // closed without an answer: nothing was promoted
		}

		if closed.GetResponderMemberId() == "" || closed.GetResponderMemberId() == closed.GetCloserMemberId() {
			return nil // self-close: the responder wrote the resolution
		}

		targetID, err := uuid.Parse(closed.GetResponderMemberId())
		if err != nil {
			logger.WarnContext(msg.Context(), "closure notifier: malformed responder id",
				slog.String("value", closed.GetResponderMemberId()), slog.Any("error", err))

			return nil // poison message: log and drop
		}

		responder, err := members.ByID(msg.Context(), targetID)
		if err != nil {
			logger.WarnContext(msg.Context(), "closure notifier: cannot resolve responder",
				slog.String("member_id", targetID.String()), slog.Any("error", err))

			return nil
		}

		if responder.Email == "" {
			return nil
		}

		if err := mailer.Send(msg.Context(), closureEmail(responder.DisplayName, responder.Email, closed, baseURL)); err != nil {
			// Transient transport failure — return so the router retries.
			return fmt.Errorf("closure notifier: %w", err)
		}

		logger.InfoContext(msg.Context(), "closure notification sent",
			slog.String("member_id", targetID.String()),
			slog.String("conversation_id", closed.GetConversationId()))

		return nil
	}
}

// closureEmail summarizes the distilled resolution and links the conversation
// so the responder can review — and correct the resolution if the distillation
// missed the point.
func closureEmail(displayName, email string, closed *orakov1.ConversationClosed, baseURL string) model.EmailMessage {
	convURL := strings.TrimRight(baseURL, "/") + "/conversations/" + closed.GetConversationId()

	name := strings.TrimSpace(displayName)
	if name == "" {
		name = "there"
	}

	var text strings.Builder

	fmt.Fprintf(&text, "Hi %s,\n\n", name)
	text.WriteString("The agent distilled your answer and closed the conversation. This is what entered the team's history:\n\n")
	fmt.Fprintf(&text, "%s\n\n", closed.GetResolution())
	fmt.Fprintf(&text, "Review the conversation: %s\n\n", convURL)
	text.WriteString("If the distillation misses the point, open the conversation and correct it.\n\n— Orako\n")

	esc := template.HTMLEscapeString

	html := `<!DOCTYPE html><html><body style="margin:0;padding:24px;background:#F7F8FA;font-family:-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
<div style="max-width:520px;margin:0 auto;background:#fff;border:1px solid #E9EBEE;border-radius:14px;padding:28px;">
<h2 style="margin:0 0 10px;font-size:17px;color:#171B24;">Your answer is now team knowledge</h2>
<p style="margin:0 0 14px;font-size:14px;color:#6B7280;">The agent distilled your answer and closed the conversation. Here is what was saved:</p>
<blockquote style="margin:0 0 18px;padding:12px 16px;background:#F4F3FD;border-left:3px solid #5850EC;border-radius:6px;font-size:14px;color:#3A414D;">` + esc(closed.GetResolution()) + `</blockquote>
<a href="` + esc(convURL) + `" style="font-size:14px;color:#5850EC;font-weight:600;text-decoration:none;">Review the conversation →</a>
<p style="margin:16px 0 0;font-size:12.5px;color:#9AA1AC;">If the distillation misses the point, open the conversation and correct it.</p>
</div></body></html>`

	return model.EmailMessage{
		To:       email,
		Subject:  "Your answer was saved to the team history",
		TextBody: text.String(),
		HTMLBody: html,
	}
}
