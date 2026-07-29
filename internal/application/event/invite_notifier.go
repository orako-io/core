// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/adapters/messaging"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

// projectByIDReader resolves a project so the invite email can name the owning
// organization. *identity.ProjectStore satisfies it.
type projectByIDReader interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Project, error)
}

// orgByIDReader resolves an organization name. *identity.OrganizationStore
// satisfies it.
type orgByIDReader interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Organization, error)
}

// InviteNotifier is the async subscriber that emails a newly invited member
// their join link. It reacts to MEMBER_LIFECYCLE(INVITED) off the event stream
// rather than sending inline in AddMember, so a slow or failing SMTP server
// never blocks the invite; a transient failure returns an error and the router
// retries.
func InviteNotifier(
	members memberByIDReader,
	projects projectByIDReader,
	orgs orgByIDReader,
	links service.InviteLinkGenerator,
	mailer service.Mailer,
	baseURL string,
	logger *slog.Logger,
) message.NoPublishHandlerFunc {
	return func(msg *message.Message) error {
		env, err := messaging.DecodeEnvelope(msg.Payload)
		if err != nil {
			return err
		}

		if env.GetType() != orakov1.EventType_EVENT_TYPE_MEMBER_LIFECYCLE {
			return nil
		}

		lifecycle := env.GetMemberLifecycle()
		if lifecycle == nil || lifecycle.GetTransition() != orakov1.MemberTransition_MEMBER_TRANSITION_INVITED {
			return nil
		}

		memberID, err := uuid.Parse(lifecycle.GetMemberId())
		if err != nil {
			// Malformed id is a poison message: returning the error would retry
			// it forever. Log and drop instead.
			logger.WarnContext(msg.Context(), "invite notifier: malformed member id",
				slog.String("value", lifecycle.GetMemberId()), slog.Any("error", err))

			return nil
		}

		member, err := members.ByID(msg.Context(), memberID)
		if err != nil {
			logger.WarnContext(msg.Context(), "invite notifier: cannot resolve member",
				slog.String("member_id", memberID.String()), slog.Any("error", err))

			return nil
		}

		if member.Email == "" {
			// Invited without an email (e.g. Slack-sourced binding) — nothing to send.
			return nil
		}

		orgName := orgNameForProject(msg.Context(), projects, orgs, lifecycle.GetProjectId())
		inviteURL, tokenized := inviteLink(msg.Context(), links, member, orgName, baseURL, logger)

		if err := mailer.Send(msg.Context(), invitationEmail(member, orgName, inviteURL, tokenized)); err != nil {
			if isPermanentMailError(err) {
				// A permanent rejection (SMTP 5xx: bad recipient, mailbox
				// unavailable) will fail identically on every retry — returning
				// the error would spin a poison loop that also re-hammers the
				// invite-link generator (the 2026-07 invite-loop incident). Log
				// and drop; the admin can fix the address and re-invite.
				logger.WarnContext(msg.Context(), "invite notifier: permanent mail failure; dropping invite",
					slog.String("member_id", memberID.String()), slog.Any("error", err))

				return nil
			}

			// Transient transport failure (network, 4xx) — return so the router retries.
			return fmt.Errorf("invite notifier: %w", err)
		}

		logger.InfoContext(msg.Context(), "invitation email sent",
			slog.String("member_id", memberID.String()))

		return nil
	}
}

// orgNameForProject best-effort resolves the organization name owning
// projectID. Any failure falls back to "your team" — the email still works.
func orgNameForProject(ctx context.Context, projects projectByIDReader, orgs orgByIDReader, projectID string) string {
	const fallback = "your team"

	pid, err := uuid.Parse(projectID)
	if err != nil {
		return fallback
	}

	project, err := projects.ByID(ctx, pid)
	if err != nil || project.OrgID == uuid.Nil {
		return fallback
	}

	org, err := orgs.ByID(ctx, project.OrgID)
	if err != nil || strings.TrimSpace(org.Name) == "" {
		return fallback
	}

	return org.Name
}

// isPermanentMailError reports whether a mail send failure is permanent — an
// SMTP 5xx server rejection (e.g. an invalid recipient), which fails identically
// on every retry. Transient failures (network, dial, 4xx) return false and stay
// retryable.
func isPermanentMailError(err error) bool {
	var protoErr *textproto.Error
	if errors.As(err, &protoErr) {
		return protoErr.Code >= 500
	}

	return false
}

// inviteLink resolves the URL the invitation button points at: a signed
// /auth/callback link when a generator is configured (the click signs the
// invitee in directly), else the tokenless /invite signup URL. A generation
// failure degrades to the fallback — the email must still go out.
func inviteLink(
	ctx context.Context,
	links service.InviteLinkGenerator,
	member model.Member,
	orgName, baseURL string,
	logger *slog.Logger,
) (string, bool) {
	base := strings.TrimRight(baseURL, "/")

	fallback := base + "/invite?" + url.Values{
		"org":   {orgName},
		"email": {member.Email},
	}.Encode()

	if links == nil {
		return fallback, false
	}

	token, linkType, err := links.GenerateInviteLink(ctx, member.Email)
	if err != nil {
		logger.WarnContext(ctx, "invite notifier: action link generation failed; sending tokenless link",
			slog.String("member_id", member.ID.String()), slog.Any("error", err))

		return fallback, false
	}

	return base + "/auth/callback?" + url.Values{
		"token_hash": {token},
		"type":       {linkType},
		"email":      {member.Email}, // display prefill only, never trusted
	}.Encode(), true
}

// invitationEmail builds the "you're invited" email with the acceptance link.
// The HTML mirrors the dashboard's invite-acceptance screen (logo tile, card,
// indigo button); the text part carries the same content for plain clients.
func invitationEmail(member model.Member, orgName, inviteURL string, tokenized bool) model.EmailMessage {
	joinSentence := "Accept the invitation, sign up with this email address, and questions will reach you where you choose."
	if tokenized {
		joinSentence = "Accepting signs you in directly — pick a password and questions will reach you where you choose."
	}

	var text strings.Builder

	fmt.Fprintf(&text, "Hi %s,\n\n", displayNameOrThere(member))
	fmt.Fprintf(&text, "You've been invited to join %s on Orako.\n\n", orgName)
	text.WriteString("Orako connects your team's experts to the coding agents that need answers. ")
	text.WriteString(joinSentence + "\n\n")
	fmt.Fprintf(&text, "Accept the invitation: %s\n\n", inviteURL)
	text.WriteString("— Orako\n")

	return model.EmailMessage{
		To:       member.Email,
		Subject:  fmt.Sprintf("You've been invited to join %s on Orako", orgName),
		TextBody: text.String(),
		HTMLBody: invitationHTML(orgName, inviteURL, joinSentence),
	}
}

// invitationHTML renders the invitation as email-client-safe HTML (tables +
// inline styles), visually consistent with the Orako design system.
func invitationHTML(orgName, inviteURL, joinSentence string) string {
	esc := template.HTMLEscapeString

	return `<!DOCTYPE html>
<html><body style="margin:0;padding:0;background-color:#F7F8FA;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background-color:#F7F8FA;padding:40px 16px;">
<tr><td align="center">
  <table role="presentation" width="440" cellpadding="0" cellspacing="0" style="max-width:440px;width:100%;background-color:#FFFFFF;border:1px solid #E9EBEE;border-radius:18px;">
  <tr><td align="center" style="padding:40px 36px 32px;font-family:-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
    <table role="presentation" cellpadding="0" cellspacing="0"><tr>
      <td align="center" style="width:44px;height:44px;border-radius:13px;background-color:#5850EC;font-size:22px;line-height:44px;color:#FFFFFF;font-weight:700;">&#8644;</td>
    </tr></table>
    <h1 style="margin:22px 0 0;font-size:22px;line-height:1.3;font-weight:700;color:#171B24;">
      You&rsquo;re invited to join ` + esc(orgName) + `
    </h1>
    <p style="margin:12px 0 0;font-size:14.5px;line-height:1.6;color:#6B7280;">
      Orako connects your team&rsquo;s experts to the coding agents that need answers.
      ` + esc(joinSentence) + `
    </p>
    <table role="presentation" cellpadding="0" cellspacing="0" width="100%" style="margin-top:26px;"><tr>
      <td align="center" style="border-radius:11px;background-color:#5850EC;">
        <a href="` + esc(inviteURL) + `" style="display:inline-block;padding:14px 24px;width:100%;box-sizing:border-box;font-size:15px;font-weight:600;color:#FFFFFF;text-decoration:none;font-family:-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">Accept invitation</a>
      </td>
    </tr></table>
    <p style="margin:16px 0 0;font-size:12px;line-height:1.5;color:#9AA1AC;">
      By accepting you agree to Orako&rsquo;s Terms and Privacy Policy.
    </p>
  </td></tr>
  </table>
  <p style="margin:20px 0 0;font-size:12px;color:#9AA1AC;font-family:-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
    If the button doesn&rsquo;t work, copy this link: ` + esc(inviteURL) + `<br>&mdash; Orako
  </p>
</td></tr>
</table>
</body></html>`
}
