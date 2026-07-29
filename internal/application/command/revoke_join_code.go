// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// RevokeJoinCodeCommand revokes the org's live join code. OrgID comes from the
// authenticated caller (never a request field). Idempotent: revoking when no
// live code exists is a success, not an error.
type RevokeJoinCodeCommand struct {
	OrgID uuid.UUID
}

// joinCodeRevoker revokes an org's live join code(s). Idempotent.
// *identity.JoinTokenStore satisfies it.
type joinCodeRevoker interface {
	RevokeJoinToken(ctx context.Context, orgID uuid.UUID) error
}

// RevokeJoinCodeHandler handles RevokeJoinCodeCommand.
type RevokeJoinCodeHandler struct {
	tokens joinCodeRevoker
}

// MustNewRevokeJoinCodeHandler builds the handler. It panics on a nil
// dependency, per project convention.
func MustNewRevokeJoinCodeHandler(tokens joinCodeRevoker) RevokeJoinCodeHandler {
	if tokens == nil {
		panic("RevokeJoinCodeHandler requires a non-nil join-code revoker")
	}

	return RevokeJoinCodeHandler{tokens: tokens}
}

// Handle revokes the caller org's live join code.
func (h RevokeJoinCodeHandler) Handle(ctx context.Context, cmd RevokeJoinCodeCommand) error {
	if cmd.OrgID == uuid.Nil {
		return errs.InvalidError{Field: "org_id", Reason: reasonNilUUID}
	}

	if err := h.tokens.RevokeJoinToken(ctx, cmd.OrgID); err != nil {
		return translateErr(err, "join_code")
	}

	return nil
}
