// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/errs"
)

// clearSentinel is the wire convention for clearing an optional field that
// would otherwise be indistinguishable from "leave unchanged": empty string
// means unchanged, the literal "-" clears it. Mirrors
// UpdateOrganizationSettingsRequest.default_alert_channel_id.
const clearSentinel = "-"

// memberChannelStore is the narrow read+write port UpdateMember needs. It is
// satisfied by *identity.MemberStore.
type memberChannelStore interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Member, error)
	Update(ctx context.Context, member model.Member) error
}

// memberOrgReader resolves the organizations a member belongs to, via their
// project memberships — the only way to establish a member's tenant, since
// Member itself carries no org_id. *identity.ProjectStore satisfies it (the
// same ProjectsByMember port the JWT principal resolver uses for the
// caller's own org).
type memberOrgReader interface {
	ProjectsByMember(ctx context.Context, memberID uuid.UUID) ([]repository.ProjectWithRole, error)
}

// UpdateMemberCommand updates a member's contact settings. Empty string
// fields are treated as "leave unchanged" (proto3 cannot distinguish empty from
// unset). The four channel bindings (Slack/Telegram/Teams/Discord) and
// GitHandle additionally accept the clearSentinel ("-") to CLEAR the value —
// a UX/PM profile has no git identity, and a wrongly-set binding (e.g. a
// Discord ID pasted on the wrong account) must be removable.
type UpdateMemberCommand struct {
	// MemberID is the caller (resolved from the auth token).
	MemberID uuid.UUID
	// TargetMemberID, when set to a member other than MemberID, requires
	// IsOrgAdmin — an org admin filling in a member's card. Zero value means
	// "the caller" (self-service, the default path).
	TargetMemberID uuid.UUID `exhaustruct:"optional"`
	// IsOrgAdmin gates TargetMemberID; resolved from org_members, never taken
	// from the request.
	IsOrgAdmin bool `exhaustruct:"optional"`
	// OrgID is the caller's own organization (CallerIdentity.OrgID), never
	// taken from the request. Required (and checked) only on the
	// TargetMemberID admin path: it scopes which members an org admin may
	// reach, closing the cross-tenant IDOR a bare IsOrgAdmin check would leave
	// open (an admin of org A must not read/write a member of org B).
	OrgID uuid.UUID `exhaustruct:"optional"`
	// DisplayName, when non-empty, replaces the display name.
	DisplayName string
	// DeliveryChannel, when non-empty, sets the contact channel. It must be a
	// valid channel and, for an external channel, the matching binding must end
	// up present.
	DeliveryChannel model.DeliveryChannel
	// SlackUserID, when non-empty, sets the Slack binding; clearSentinel clears it.
	SlackUserID string
	// TelegramChatID, when non-empty, sets the Telegram binding; clearSentinel
	// clears it.
	TelegramChatID string
	// TeamsUserID, when non-empty, sets the Teams binding; clearSentinel clears it.
	TeamsUserID string
	// DiscordUserID, when non-empty, sets the Discord binding; clearSentinel
	// clears it.
	DiscordUserID string
	// Email, when non-empty, replaces the member's contact email.
	Email string
	// FirstName / LastName / GitHandle, when non-empty, replace the profile fields.
	FirstName string
	LastName  string
	GitHandle string
}

// UpdateMemberHandler handles UpdateMemberCommand.
type UpdateMemberHandler struct {
	store memberChannelStore
	orgs  memberOrgReader
}

// MustNewUpdateMemberHandler builds a handler. It panics on nil
// dependencies.
func MustNewUpdateMemberHandler(store memberChannelStore, orgs memberOrgReader) UpdateMemberHandler {
	if store == nil {
		panic("UpdateMemberHandler requires a non-nil memberChannelStore")
	}

	if orgs == nil {
		panic("UpdateMemberHandler requires a non-nil memberOrgReader")
	}

	return UpdateMemberHandler{store: store, orgs: orgs}
}

// Handle resolves the target member (self, or another when org-admin gated),
// loads them, applies the provided changes, validates the resulting
// channel/binding invariant, and persists. It returns the updated member so
// the caller need not re-read.
func (h UpdateMemberHandler) Handle(ctx context.Context, cmd UpdateMemberCommand) (model.Member, error) {
	target := cmd.MemberID

	if cmd.TargetMemberID != uuid.Nil && cmd.TargetMemberID != cmd.MemberID {
		if !cmd.IsOrgAdmin {
			return model.Member{}, errs.ForbiddenError{Action: "update another member's settings"}
		}

		// Tenant scope: an org admin may only reach a member of their OWN
		// organization. This must run BEFORE any load of the target — a
		// global ByID lookup keyed on admin-status alone would let an org-A
		// admin read/write any member in the database by UUID.
		if err := h.verifyTargetInCallerOrg(ctx, cmd.OrgID, cmd.TargetMemberID); err != nil {
			return model.Member{}, err
		}

		target = cmd.TargetMemberID
	}

	member, err := h.store.ByID(ctx, target)
	if err != nil {
		return model.Member{}, translateErr(err, "member")
	}

	if err := applyProfile(&member, cmd); err != nil {
		return model.Member{}, err
	}

	bindingChanged := applyBindings(&member, cmd)

	if cmd.DeliveryChannel != "" {
		if !cmd.DeliveryChannel.Valid() {
			return model.Member{}, errs.InvalidError{Field: "delivery_channel", Reason: "unknown channel"}
		}

		member.DeliveryChannel = cmd.DeliveryChannel
	}

	if bindingChanged {
		member.BindingError = ""
	}

	if err := validateChannelBinding(member); err != nil {
		return model.Member{}, err
	}

	if err := h.store.Update(ctx, member); err != nil {
		return model.Member{}, translateErr(err, "member")
	}

	return member, nil
}

// verifyTargetInCallerOrg proves targetMemberID belongs to callerOrgID before
// any load or write of the target member is attempted. A caller with no
// resolved org (callerOrgID == uuid.Nil), or a target with no membership in
// that org, is rejected — fail closed rather than fail open on an
// unresolved/foreign tenant.
func (h UpdateMemberHandler) verifyTargetInCallerOrg(ctx context.Context, callerOrgID, targetMemberID uuid.UUID) error {
	if callerOrgID == uuid.Nil {
		return errs.ForbiddenError{Action: "update another member's settings"}
	}

	memberships, err := h.orgs.ProjectsByMember(ctx, targetMemberID)
	if err != nil {
		return translateErr(err, "member")
	}

	for _, m := range memberships {
		if m.OrgID == callerOrgID {
			return nil
		}
	}

	return errs.ForbiddenError{Action: "update another member's settings"}
}

// applyProfile applies the non-empty identity/profile fields of cmd to member.
func applyProfile(member *model.Member, cmd UpdateMemberCommand) error {
	if strings.TrimSpace(cmd.DisplayName) != "" {
		member.DisplayName = cmd.DisplayName
	}

	if email := strings.TrimSpace(cmd.Email); email != "" {
		if !strings.Contains(email, "@") {
			return errs.InvalidError{Field: "email", Reason: "must be a valid email address"}
		}

		member.Email = email
	}

	if strings.TrimSpace(cmd.FirstName) != "" {
		member.FirstName = cmd.FirstName
	}

	if strings.TrimSpace(cmd.LastName) != "" {
		member.LastName = cmd.LastName
	}

	// GitHandle is clearable ("-"): a UX/PM profile has no git identity, so a
	// handle set by mistake must be removable (empty still means unchanged).
	if v, ok := resolveClearable(cmd.GitHandle); ok {
		member.GitHandle = strings.TrimPrefix(strings.TrimSpace(v), "@")
	}

	return nil
}

// applyBindings applies the connector binding fields of cmd to member and
// reports whether any binding actually changed (so the caller can clear a
// stale binding_error). All four bindings follow the same convention: empty =
// unchanged, clearSentinel ("-") = clear.
func applyBindings(member *model.Member, cmd UpdateMemberCommand) bool {
	changed := false

	if v, ok := resolveClearable(cmd.SlackUserID); ok {
		member.SlackUserID = v
		changed = true
	}

	if v, ok := resolveClearable(cmd.TelegramChatID); ok {
		member.TelegramChatID = v
		changed = true
	}

	if v, ok := resolveClearable(cmd.TeamsUserID); ok {
		member.TeamsUserID = v
		changed = true
	}

	if v, ok := resolveClearable(cmd.DiscordUserID); ok {
		member.DiscordUserID = v
		changed = true
	}

	return changed
}

// resolveClearable applies the "empty = unchanged, '-' = clear" convention.
// ok is false when the field is left unchanged.
func resolveClearable(v string) (value string, ok bool) {
	switch v {
	case "":
		return "", false
	case clearSentinel:
		return "", true
	default:
		return v, true
	}
}

// validateChannelBinding enforces that a member's chosen channel has the binding
// it needs to actually route, so a member is never set to a channel that would
// orphan their questions at delivery time.
func validateChannelBinding(m model.Member) error {
	switch m.DeliveryChannel {
	case model.DeliveryChannelSlack:
		if strings.TrimSpace(m.SlackUserID) == "" {
			return errs.InvalidError{Field: "slack_user_id", Reason: "required for the slack channel"}
		}
	case model.DeliveryChannelTelegram:
		if strings.TrimSpace(m.TelegramChatID) == "" {
			return errs.InvalidError{Field: "telegram_chat_id", Reason: "required for the telegram channel"}
		}
	case model.DeliveryChannelTeams:
		if strings.TrimSpace(m.TeamsUserID) == "" {
			return errs.InvalidError{Field: "teams_user_id", Reason: "required for the teams channel"}
		}
	case model.DeliveryChannelDiscord:
		if strings.TrimSpace(m.DiscordUserID) == "" {
			return errs.InvalidError{Field: "discord_user_id", Reason: "required for the discord channel"}
		}
	case model.DeliveryChannelDashboard, "":
		// The dashboard channel needs no binding; empty defaults to dashboard.
	}

	return nil
}
