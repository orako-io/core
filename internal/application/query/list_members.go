// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"

	"github.com/google/uuid"
)

// OrgMemberReader reads the rich org roster. *identity.MemberStore satisfies it.
type OrgMemberReader interface {
	ListOrgMembers(ctx context.Context, orgID uuid.UUID) ([]OrgMemberView, error)
	// ListPendingOrgMembers returns the org's self-join members awaiting approval
	// (status 'pending'), which ListOrgMembers deliberately omits.
	ListPendingOrgMembers(ctx context.Context, orgID uuid.UUID) ([]OrgMemberView, error)
	OrgMemberByID(ctx context.Context, orgID, memberID uuid.UUID) (OrgMemberView, error)
}

// ListMembersQuery is the input for the org roster (the caller's active org).
type ListMembersQuery struct {
	OrgID uuid.UUID
	// IncludePending appends the org's pending self-join members (join-code
	// redeemers awaiting approval) to the roster. It is an admin-only view: the
	// caller (the API server) sets it ONLY when the caller is an org admin, so
	// pending members never reach a non-admin through this query. The roster
	// itself (status active/on_leave/deactivated/invited) never contains pending.
	IncludePending bool `exhaustruct:"optional"`
}

// ListMembersHandler handles ListMembersQuery.
type ListMembersHandler struct {
	reader OrgMemberReader
}

// MustNewListMembersHandler builds a handler.
func MustNewListMembersHandler(reader OrgMemberReader) ListMembersHandler {
	if reader == nil {
		panic("ListMembersHandler requires a non-nil OrgMemberReader")
	}

	return ListMembersHandler{reader: reader}
}

// Handle returns the caller's org roster (active/on_leave/deactivated/invited).
// When q.IncludePending is set (admin-only, gated by the caller), the org's
// pending self-join members are appended so an admin can approve or reject them.
func (h ListMembersHandler) Handle(ctx context.Context, q ListMembersQuery) ([]OrgMemberView, error) {
	roster, err := h.reader.ListOrgMembers(ctx, q.OrgID)
	if err != nil {
		return nil, translateReadError(err, "members")
	}

	if !q.IncludePending {
		return roster, nil
	}

	pending, err := h.reader.ListPendingOrgMembers(ctx, q.OrgID)
	if err != nil {
		return nil, translateReadError(err, "members")
	}

	return append(roster, pending...), nil
}
