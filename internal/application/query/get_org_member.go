// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"

	"github.com/google/uuid"
)

// GetOrgMemberQuery fetches one member's rich record, scoped to the caller's org.
type GetOrgMemberQuery struct {
	OrgID    uuid.UUID
	MemberID uuid.UUID
}

// GetOrgMemberHandler handles GetOrgMemberQuery.
type GetOrgMemberHandler struct {
	reader OrgMemberReader
}

// MustNewGetOrgMemberHandler builds a handler.
func MustNewGetOrgMemberHandler(reader OrgMemberReader) GetOrgMemberHandler {
	if reader == nil {
		panic("GetOrgMemberHandler requires a non-nil OrgMemberReader")
	}

	return GetOrgMemberHandler{reader: reader}
}

// Handle returns the member, or the adapter's not-found error when the member is
// not part of the caller's org.
func (h GetOrgMemberHandler) Handle(ctx context.Context, q GetOrgMemberQuery) (OrgMemberView, error) {
	member, err := h.reader.OrgMemberByID(ctx, q.OrgID, q.MemberID)
	if err != nil {
		return OrgMemberView{}, translateReadError(err, "member")
	}

	return member, nil
}
