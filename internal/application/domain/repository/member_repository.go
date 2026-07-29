// SPDX-License-Identifier: AGPL-3.0-or-later

package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// MemberEmail pairs a member id with their email — the minimal shape the Slack
// directory backfill reads (SyncChatDirectory).
type MemberEmail struct {
	ID    uuid.UUID
	Email string
}

// MemberRepository is the write-side port for member identity persistence.
// Driven adapters under internal/adapters implement it; the domain owns the
// contract.
//
// Implementations expose only the sentinel errors from
// internal/adapters/errors, never raw driver errors.
type MemberRepository interface {
	// Create durably stores a new member. Returns adaptererr.ErrDuplicate when
	// a member with the same ID already exists.
	Create(ctx context.Context, member model.Member) error
	// ByID fetches a member by its UUID. Returns adaptererr.ErrNotFound when no
	// member with that ID exists.
	ByID(ctx context.Context, id uuid.UUID) (model.Member, error)
	// ByEmail fetches a member by their email address. Returns
	// adaptererr.ErrNotFound when no member with that email exists.
	ByEmail(ctx context.Context, email string) (model.Member, error)
	// Update persists mutations to an existing member (display_name, status,
	// slack_user_id, updated_at). Returns adaptererr.ErrNotFound when absent.
	Update(ctx context.Context, member model.Member) error
	// OffboardFromOrg removes the member's project and authority links only in
	// orgID. The member is transitioned to removed/purged and its chat bindings
	// are freed only when it has no membership in another organization.
	// Returns adaptererr.ErrNotFound when the member has no membership in orgID.
	OffboardFromOrg(ctx context.Context, member model.Member, orgID uuid.UUID) error
}
