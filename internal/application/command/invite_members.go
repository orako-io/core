// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"
)

// memberAdder adds one member to a project. AddMemberHandler satisfies it.
type memberAdder interface {
	Handle(ctx context.Context, cmd AddMemberCommand) (AddMemberResult, error)
}

// InviteMembersCommand invites a list of emails to a project in one call, so a
// team owner pastes a list instead of adding people one by one. Role/domain
// config stays optional and deferred — the point is to get everyone in fast.
type InviteMembersCommand struct {
	ProjectID uuid.UUID
	Emails    []string
	Domains   []string `exhaustruct:"optional"`
}

// InviteOutcome is the per-email result of a bulk invite.
type InviteOutcome struct {
	Email  string
	Status string // "invited" | "already" | "invalid" | "error"
}

// InviteMembersResult is the per-email summary.
type InviteMembersResult struct {
	Results []InviteOutcome
}

// InviteMembersHandler handles InviteMembersCommand.
type InviteMembersHandler struct {
	adder memberAdder
}

// MustNewInviteMembersHandler builds a handler. It panics on nil deps.
func MustNewInviteMembersHandler(adder memberAdder) InviteMembersHandler {
	if adder == nil {
		panic("InviteMembersHandler requires a non-nil member adder")
	}

	return InviteMembersHandler{adder: adder}
}

// Handle invites each email best-effort: an invalid or failed one is reported
// in the summary, it never aborts the batch. De-duplicates the input so the
// same email is invited once.
func (h InviteMembersHandler) Handle(ctx context.Context, cmd InviteMembersCommand) (InviteMembersResult, error) {
	seen := make(map[string]bool)
	result := InviteMembersResult{Results: make([]InviteOutcome, 0, len(cmd.Emails))}

	for _, raw := range cmd.Emails {
		email := strings.ToLower(strings.TrimSpace(raw))
		if email == "" || seen[email] {
			continue
		}

		seen[email] = true

		if !looksLikeEmail(email) {
			result.Results = append(result.Results, InviteOutcome{Email: email, Status: "invalid"})

			continue
		}

		res, err := h.adder.Handle(ctx, AddMemberCommand{ProjectID: cmd.ProjectID, Email: email, DisplayName: email, Domains: cmd.Domains})
		switch {
		case err != nil:
			slog.WarnContext(ctx, "invite_members: adding member", slog.String("email", email), slog.Any("error", err))
			result.Results = append(result.Results, InviteOutcome{Email: email, Status: "error"})
		case res.AlreadyMember:
			result.Results = append(result.Results, InviteOutcome{Email: email, Status: "already"})
		default:
			result.Results = append(result.Results, InviteOutcome{Email: email, Status: "invited"})
		}
	}

	return result, nil
}

// looksLikeEmail is a cheap shape check (one @, a dot after it) — the real
// validation is the identity provider's; this only filters obvious junk from a
// pasted list so it does not become a member row.
func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return false
	}

	return strings.IndexByte(s[at+1:], '.') > 0
}
