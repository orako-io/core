// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// memberAccountResolver resolves a member's login account. *identity.MemberStore
// satisfies it.
type memberAccountResolver interface {
	AccountID(ctx context.Context, memberID uuid.UUID) (uuid.UUID, bool, error)
}

// orgAdminStore reads and writes org-level roles. *identity.OrganizationStore
// satisfies it.
type orgAdminStore interface {
	RoleFor(ctx context.Context, orgID, accountID uuid.UUID) (model.OrgRole, error)
	AdminCount(ctx context.Context, orgID uuid.UUID) (int, error)
	AddMember(ctx context.Context, m model.OrgMembership) error
}

// SetOrgAdminCommand grants or revokes org-admin on another member.
type SetOrgAdminCommand struct {
	OrgID    uuid.UUID
	MemberID uuid.UUID
	IsAdmin  bool
}

// SetOrgAdminHandler handles SetOrgAdminCommand.
type SetOrgAdminHandler struct {
	members memberAccountResolver
	orgs    orgAdminStore
}

// MustNewSetOrgAdminHandler builds a handler.
func MustNewSetOrgAdminHandler(members memberAccountResolver, orgs orgAdminStore) SetOrgAdminHandler {
	if members == nil || orgs == nil {
		panic("SetOrgAdminHandler requires non-nil dependencies")
	}

	return SetOrgAdminHandler{members: members, orgs: orgs}
}

// Handle grants or revokes org-admin. Only a member with a login account can be
// an org admin. Revoking the last remaining admin is refused so an org is never
// left without one.
func (h SetOrgAdminHandler) Handle(ctx context.Context, cmd SetOrgAdminCommand) error {
	accountID, ok, err := h.members.AccountID(ctx, cmd.MemberID)
	if err != nil {
		return translateErr(err, "member")
	}

	if !ok {
		return errs.InvalidError{Field: fieldMemberID, Reason: "an external member without a login cannot be an org admin"}
	}

	if !cmd.IsAdmin {
		if err := h.guardLastAdmin(ctx, cmd.OrgID, accountID); err != nil {
			return err
		}
	}

	role := model.OrgRoleMember
	if cmd.IsAdmin {
		role = model.OrgRoleAdmin
	}

	if err := h.orgs.AddMember(ctx, model.OrgMembership{OrgID: cmd.OrgID, AccountID: accountID, Role: role}); err != nil {
		return translateErr(err, "org membership")
	}

	return nil
}

// guardLastAdmin refuses to demote the org's only remaining admin.
func (h SetOrgAdminHandler) guardLastAdmin(ctx context.Context, orgID, accountID uuid.UUID) error {
	role, err := h.orgs.RoleFor(ctx, orgID, accountID)
	if err != nil {
		return translateErr(err, "org membership")
	}

	if role != model.OrgRoleAdmin {
		return nil
	}

	count, err := h.orgs.AdminCount(ctx, orgID)
	if err != nil {
		return translateErr(err, "org membership")
	}

	if count <= 1 {
		return errs.InvalidError{Field: "is_admin", Reason: "cannot remove the last org admin — promote someone else first"}
	}

	return nil
}
