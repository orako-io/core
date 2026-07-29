// SPDX-License-Identifier: AGPL-3.0-or-later

package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// JoinToken is a shareable org join code resolved to the org/project a
// redemption lands in. The org and project come ONLY from this row — never from
// caller input — so a stranger who redeems a code can only ever land in the
// org/project the code was minted for.
type JoinToken struct {
	// Token is the opaque, URL-safe code the person pastes at consent. Empty on a
	// JoinTokenByToken result (the caller already holds it).
	Token string `exhaustruct:"optional"`
	// OrgID / ProjectID are the landing org and project.
	OrgID     uuid.UUID
	ProjectID uuid.UUID
	// Revoked reports whether the token has been revoked (JoinTokenByToken only;
	// ActiveJoinToken never returns a revoked row).
	Revoked bool `exhaustruct:"optional"`
	// CreatedAt is when the token was minted (ActiveJoinToken only).
	CreatedAt time.Time `exhaustruct:"optional"`
}

// JoinTokenRepository is the write/read port for shareable org join codes.
// Driven adapters under internal/adapters implement it; implementations expose
// only the sentinel errors from internal/adapters/errors.
type JoinTokenRepository interface {
	// CreateJoinToken mints a fresh live token for the org, revoking any prior
	// live token in the same transaction (at most one live code per org). It
	// returns the raw token string.
	CreateJoinToken(ctx context.Context, orgID, projectID, createdByMemberID uuid.UUID) (string, error)
	// JoinTokenByToken resolves a raw token to its org/project and revoked flag.
	// Returns adaptererr.ErrNotFound when no such token exists.
	JoinTokenByToken(ctx context.Context, token string) (JoinToken, error)
	// RevokeJoinToken revokes the org's live token(s). Idempotent.
	RevokeJoinToken(ctx context.Context, orgID uuid.UUID) error
	// ActiveJoinToken returns the org's current live token. ok is false when the
	// org has no live token.
	ActiveJoinToken(ctx context.Context, orgID uuid.UUID) (JoinToken, bool, error)
}
