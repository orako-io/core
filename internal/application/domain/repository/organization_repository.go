// SPDX-License-Identifier: AGPL-3.0-or-later

package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// OrganizationRepository is the write/read port for organizations and their
// account memberships (org-scoped RBAC). Driven adapters under internal/adapters
// implement it. Implementations expose only the sentinel errors from
// internal/adapters/errors.
type OrganizationRepository interface {
	// Create durably stores a new organization.
	Create(ctx context.Context, org model.Organization) error
	// ByID fetches an organization by its UUID. Returns adaptererr.ErrNotFound
	// when absent.
	ByID(ctx context.Context, id uuid.UUID) (model.Organization, error)
	// AddMember adds (or updates the role of) an account in an org. Idempotent.
	AddMember(ctx context.Context, membership model.OrgMembership) error
	// RoleFor returns the account's role in the org. Returns
	// adaptererr.ErrNotFound when the account is not a member.
	RoleFor(ctx context.Context, orgID, accountID uuid.UUID) (model.OrgRole, error)
	// ProjectInOrgForMember returns one project the member belongs to within the
	// given org (the org switcher's landing project). ok is false when the member
	// has no project in that org — the caller must not be scoped to it.
	ProjectInOrgForMember(ctx context.Context, memberID, orgID uuid.UUID) (projectID uuid.UUID, ok bool, err error)
}
