// SPDX-License-Identifier: AGPL-3.0-or-later

package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// ProjectMembership holds the role a member holds within a project, together
// with the optional domain tags that scope their expertise.
type ProjectMembership struct {
	ProjectID uuid.UUID
	MemberID  uuid.UUID
	Role      model.Role
	Domains   []string
}

// ProjectWithRole is a project record the caller belongs to, with the project's
// owning organization. Returned by the read-side ProjectsByMember query. Role is
// retained for the ListProjects DTO but is always unspecified since project
// memberships no longer carry a role (migration 0007); it is retired in Part 2.
type ProjectWithRole struct {
	ID    uuid.UUID
	Name  string
	Role  model.Role
	OrgID uuid.UUID // owning organization; uuid.Nil when the project has no org
	// Archived is true when the project is currently frozen (archived_at set).
	// Callers that must exclude archived projects (e.g. ListProjects) filter on
	// it; callers that resolve tenancy only (e.g. verifyTargetInCallerOrg)
	// ignore it.
	Archived bool `exhaustruct:"optional"`
}

// ProjectRepository is the write-side port for project and membership
// persistence. Driven adapters under internal/adapters implement it; the domain
// owns the contract.
//
// Implementations expose only the sentinel errors from
// internal/adapters/errors, never raw driver errors.
type ProjectRepository interface {
	// Create durably stores a new project. Returns adaptererr.ErrDuplicate when
	// a project with the same ID already exists.
	Create(ctx context.Context, project model.Project) error
	// ByID fetches a project by its UUID. Returns adaptererr.ErrNotFound when no
	// project with that ID exists.
	ByID(ctx context.Context, id uuid.UUID) (model.Project, error)
	// AddMember records a membership. The (project_id, member_id) pair is the
	// natural key; returns adaptererr.ErrDuplicate when it already exists.
	AddMember(ctx context.Context, m ProjectMembership) error
	// SetMemberDomains replaces the expertise tags (domains) of an existing
	// membership. Returns adaptererr.ErrNotFound when the (project_id, member_id)
	// pair is not enrolled. This is the set-expertise-tags write (repurposed from
	// the retired role-assignment path).
	SetMemberDomains(ctx context.Context, projectID, memberID uuid.UUID, domains []string) error
	SetDomainsForMemberInOrg(ctx context.Context, orgID, memberID uuid.UUID, domains []string) error
	// MembersByProject lists all memberships for the given project, ordered by
	// join time ascending.
	MembersByProject(ctx context.Context, projectID uuid.UUID) ([]ProjectMembership, error)
}
