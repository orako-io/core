// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"

	"github.com/google/uuid"
)

// GetMemberQuery is the input for fetching a single member's own settings.
type GetMemberQuery struct {
	// MemberID is the caller (resolved from the auth token).
	MemberID uuid.UUID
}

// GetMemberHandler handles GetMemberQuery.
type GetMemberHandler struct {
	reader MemberReader
}

// MustNewGetMemberHandler builds a handler. It panics on nil
// dependencies.
func MustNewGetMemberHandler(reader MemberReader) GetMemberHandler {
	if reader == nil {
		panic("GetMemberHandler requires a non-nil MemberReader")
	}

	return GetMemberHandler{reader: reader}
}

// Handle returns the member's own contact settings as a MemberView.
func (h GetMemberHandler) Handle(ctx context.Context, q GetMemberQuery) (MemberView, error) {
	member, err := h.reader.ReadMember(ctx, q.MemberID)
	if err != nil {
		return MemberView{}, translateReadError(err, "member")
	}

	return member, nil
}
