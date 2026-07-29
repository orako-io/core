// SPDX-License-Identifier: AGPL-3.0-or-later

package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/pkg/pgconv"
	postgres "github.com/orako-io/core/internal/pkg/postgres"
)

// projectsByMemberSQL returns all projects the member belongs to with each
// project's owning org_id (nullable) and archived status, created_at ASC. No
// role is selected: admin authority comes from org_members.
const projectsByMemberSQL = `
SELECT p.id, p.name, p.org_id, p.archived_at IS NOT NULL AS archived
FROM projects p
JOIN project_members pm ON pm.project_id = p.id
WHERE pm.member_id = $1
ORDER BY p.created_at ASC
`

// projectsDetailedByOrgSQL backs the Projects tab: one row per project in the
// org with its archived flag and aggregate counts. Each LEFT JOIN independently
// fans out per project (members × conversations), so COUNT(DISTINCT ...) is
// required for correctness; the row volume is admin-tool-bounded (one org's
// projects), so the join cost is acceptable. The resolved count reuses the same
// conversations join, filtered to status = 'resolved' (the old KB-entry count
// was retired with the kb_entries table). include_archived (the boolean
// parameter) toggles whether archived projects are included at all.
const projectsDetailedByOrgSQL = `
SELECT p.id, p.name, p.archived_at IS NOT NULL AS archived,
       COUNT(DISTINCT pm.member_id) AS member_count,
       COUNT(DISTINCT c.id) AS conversation_count,
       COUNT(DISTINCT c.id) FILTER (WHERE c.status = 'resolved') AS resolved_count
FROM projects p
LEFT JOIN project_members pm ON pm.project_id = p.id
LEFT JOIN conversations c ON c.project_id = p.id
WHERE p.org_id = $1
  AND ($2 OR p.archived_at IS NULL)
GROUP BY p.id
ORDER BY p.created_at ASC
`

var _ repository.ProjectRepository = (*ProjectStore)(nil)

// ProjectStore is the Postgres-backed ProjectRepository.
type ProjectStore struct {
	pool *pgxpool.Pool
}

// NewProjectStore builds a ProjectStore backed by pool.
func NewProjectStore(pool *pgxpool.Pool) *ProjectStore {
	return &ProjectStore{pool: pool}
}

// Create durably stores a new project.
// Returns adaptererr.ErrDuplicate when a project with the same ID already exists.
func (s *ProjectStore) Create(ctx context.Context, project model.Project) error {
	_, err := New(postgres.Conn(ctx, s.pool)).createProject(ctx, createProjectParams{
		ID:   project.ID,
		Name: project.Name,
	})
	if err != nil {
		return fmt.Errorf("creating project: %w", adaptererr.Decode(err))
	}

	return nil
}

// CreateInOrg creates a project owned by an org (org_id set), unlike Create
// which leaves it null. Runs against the ambient transaction when the caller
// opened one, so it composes atomically with the rest of org creation.
func (s *ProjectStore) CreateInOrg(ctx context.Context, project model.Project) error {
	if err := New(postgres.Conn(ctx, s.pool)).createProjectInOrg(ctx, createProjectInOrgParams{
		ID:    project.ID,
		Name:  project.Name,
		OrgID: pgconv.UUIDOrNull(project.OrgID),
	}); err != nil {
		return fmt.Errorf("creating project in org: %w", adaptererr.Decode(err))
	}

	return nil
}

// CountByOrg returns the number of projects owned by an org. Backs the
// Community-edition single-project cap.
func (s *ProjectStore) CountByOrg(ctx context.Context, orgID uuid.UUID) (int, error) {
	n, err := New(postgres.Conn(ctx, s.pool)).countProjectsByOrg(ctx, pgconv.UUIDOrNull(orgID))
	if err != nil {
		return 0, fmt.Errorf("counting projects by org: %w", adaptererr.Decode(err))
	}

	return int(n), nil
}

// ReadProjectName returns the project's display name for the read side, without
// exposing the domain aggregate. Returns adaptererr.ErrNotFound when absent.
func (s *ProjectStore) ReadProjectName(ctx context.Context, id uuid.UUID) (string, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).projectByID(ctx, id)
	if err != nil {
		return "", fmt.Errorf("fetching project name by id: %w", adaptererr.Decode(err))
	}

	return row.Name, nil
}

// ByID fetches a project by its UUID.
// Returns adaptererr.ErrNotFound when no project with that ID exists.
func (s *ProjectStore) ByID(ctx context.Context, id uuid.UUID) (model.Project, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).projectByID(ctx, id)
	if err != nil {
		return model.Project{}, fmt.Errorf("fetching project by id: %w", adaptererr.Decode(err))
	}

	return model.Project{
		ID:         row.ID,
		Name:       row.Name,
		OrgID:      pgconv.UUIDFromPgtype(row.OrgID),
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
		ArchivedAt: pgconv.TimePtrFromTimestamptz(row.ArchivedAt),
	}, nil
}

// Rename updates the project's display name. Returns adaptererr.ErrNotFound
// when no project with that ID exists.
func (s *ProjectStore) Rename(ctx context.Context, id uuid.UUID, name string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE projects SET name = $2, updated_at = NOW() WHERE id = $1`,
		id, name,
	)
	if err != nil {
		return fmt.Errorf("renaming project: %w", adaptererr.Decode(err))
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("renaming project: %w", adaptererr.ErrNotFound)
	}

	return nil
}

// SetArchived sets or clears the project's archived_at timestamp. archived =
// true freezes the project (archived_at = NOW()); false reactivates it
// (archived_at = NULL). Returns adaptererr.ErrNotFound when no project with
// that ID exists.
func (s *ProjectStore) SetArchived(ctx context.Context, id uuid.UUID, archived bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE projects
		 SET archived_at = CASE WHEN $2 THEN NOW() ELSE NULL END, updated_at = NOW()
		 WHERE id = $1`,
		id, archived,
	)
	if err != nil {
		return fmt.Errorf("setting project archived status: %w", adaptererr.Decode(err))
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("setting project archived status: %w", adaptererr.ErrNotFound)
	}

	return nil
}

// Delete hard-deletes a project. Every dependent row (project_members,
// conversations, messages, project_providers, event_log) cascades
// via ON DELETE CASCADE foreign keys (see migration 0001/0003), so the single
// statement is sufficient; it still runs inside its own statement-level
// transaction (Postgres default) for atomicity. Returns adaptererr.ErrNotFound
// when no project with that ID exists.
func (s *ProjectStore) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting project: %w", adaptererr.Decode(err))
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("deleting project: %w", adaptererr.ErrNotFound)
	}

	return nil
}

// ProjectsDetailedByOrg returns every project in orgID with its archived
// status and aggregate stats (member/conversation/resolved counts), ordered by
// creation time ascending. includeArchived, when false, excludes archived
// projects entirely; the Projects tab opts into them via include_archived.
func (s *ProjectStore) ProjectsDetailedByOrg(ctx context.Context, orgID uuid.UUID, includeArchived bool) ([]query.ProjectDetailRow, error) {
	rows, err := s.pool.Query(ctx, projectsDetailedByOrgSQL, orgID, includeArchived)
	if err != nil {
		return nil, fmt.Errorf("listing detailed projects by org: %w", adaptererr.Decode(err))
	}
	defer rows.Close()

	results := []query.ProjectDetailRow{}

	for rows.Next() {
		var row query.ProjectDetailRow

		if err := rows.Scan(
			&row.ID, &row.Name, &row.Archived,
			&row.MemberCount, &row.ConversationCount, &row.ResolvedCount,
		); err != nil {
			return nil, fmt.Errorf("scanning detailed project row: %w", err)
		}

		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating detailed projects by org: %w", err)
	}

	return results, nil
}

// defaultProjectByOrgSQL resolves an org's default (global) project: the first
// project created for the org. CreateOrganization seeds this "default" project
// as the org's first, so created_at ASC LIMIT 1 identifies it without needing
// an explicit flag column.
const defaultProjectByOrgSQL = `
SELECT id, name, org_id, created_at, updated_at, archived_at
FROM projects
WHERE org_id = $1
ORDER BY created_at ASC
LIMIT 1
`

// DefaultProjectByOrg returns the org's default (global) project — the first
// project created for the org. Returns adaptererr.ErrNotFound when the org has
// no project. Backs the join-code generator's empty-project_id fallback.
func (s *ProjectStore) DefaultProjectByOrg(ctx context.Context, orgID uuid.UUID) (model.Project, error) {
	var (
		id         uuid.UUID
		name       string
		oid        pgtype.UUID
		createdAt  time.Time
		updatedAt  time.Time
		archivedAt pgtype.Timestamptz
	)

	if err := s.pool.QueryRow(ctx, defaultProjectByOrgSQL, orgID).
		Scan(&id, &name, &oid, &createdAt, &updatedAt, &archivedAt); err != nil {
		return model.Project{}, fmt.Errorf("fetching default project by org: %w", adaptererr.Decode(err))
	}

	return model.Project{
		ID:         id,
		Name:       name,
		OrgID:      pgconv.UUIDFromPgtype(oid),
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
		ArchivedAt: pgconv.TimePtrFromTimestamptz(archivedAt),
	}, nil
}

// AddMember records a project membership.
// Returns adaptererr.ErrDuplicate when the (project_id, member_id) pair already exists.
func (s *ProjectStore) AddMember(ctx context.Context, m repository.ProjectMembership) error {
	// A nil slice would encode as SQL NULL and violate TEXT[] NOT NULL.
	domains := m.Domains
	if domains == nil {
		domains = []string{}
	}

	// m.Role is intentionally not persisted; admin authority is org-scoped.
	err := New(postgres.Conn(ctx, s.pool)).addProjectMember(ctx, addProjectMemberParams{
		ProjectID: m.ProjectID,
		MemberID:  m.MemberID,
		Domains:   domains,
	})
	if err != nil {
		return fmt.Errorf("adding project member: %w", adaptererr.Decode(err))
	}

	return nil
}

// SetMemberDomains replaces the expertise tags of an existing membership.
// Returns adaptererr.ErrNotFound when the (project_id, member_id) pair is not
// enrolled (zero rows updated).
func (s *ProjectStore) SetMemberDomains(ctx context.Context, projectID, memberID uuid.UUID, domains []string) error {
	// A nil slice would encode as SQL NULL and violate TEXT[] NOT NULL.
	if domains == nil {
		domains = []string{}
	}

	rows, err := New(postgres.Conn(ctx, s.pool)).updateProjectMemberDomains(ctx, updateProjectMemberDomainsParams{
		ProjectID: projectID,
		MemberID:  memberID,
		Domains:   domains,
	})
	if err != nil {
		return fmt.Errorf("updating project member domains: %w", adaptererr.Decode(err))
	}

	if rows == 0 {
		return fmt.Errorf("updating project member domains: %w", adaptererr.ErrNotFound)
	}

	return nil
}

// SetDomainsForMember replaces the member's expertise tags across ALL their
// project memberships in one statement (self-serve onboarding). Zero rows
// updated — the member belongs to no project — is a no-op success, not
// ErrNotFound: setting expertise before joining anything is harmless.
func (s *ProjectStore) SetDomainsForMember(ctx context.Context, memberID uuid.UUID, domains []string) error {
	// A nil slice would encode as SQL NULL and violate TEXT[] NOT NULL.
	if domains == nil {
		domains = []string{}
	}

	if _, err := New(postgres.Conn(ctx, s.pool)).setDomainsForMember(ctx, setDomainsForMemberParams{
		MemberID: memberID,
		Domains:  domains,
	}); err != nil {
		return fmt.Errorf("setting domains for member: %w", adaptererr.Decode(err))
	}

	return nil
}

// MembersByProject lists all memberships for projectID, ordered by join time ascending.
func (s *ProjectStore) MembersByProject(ctx context.Context, projectID uuid.UUID) ([]repository.ProjectMembership, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).projectMembersByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("listing project members: %w", adaptererr.Decode(err))
	}

	memberships := make([]repository.ProjectMembership, len(rows))
	for i, r := range rows {
		memberships[i] = repository.ProjectMembership{
			ProjectID: r.ProjectID,
			MemberID:  r.MemberID,
			Role:      model.RoleUnspecified, // no role stored on project memberships
			Domains:   r.Domains,
		}
	}

	return memberships, nil
}

// ProjectsByMember lists all projects memberID belongs to, with each project's
// owning org_id, ordered by project creation time ascending.
func (s *ProjectStore) ProjectsByMember(ctx context.Context, memberID uuid.UUID) ([]repository.ProjectWithRole, error) {
	rows, err := s.pool.Query(ctx, projectsByMemberSQL, memberID)
	if err != nil {
		return nil, fmt.Errorf("listing projects by member: %w", adaptererr.Decode(err))
	}

	defer rows.Close()

	var results []repository.ProjectWithRole

	for rows.Next() {
		var (
			id       uuid.UUID
			name     string
			orgID    *uuid.UUID // projects.org_id is nullable
			archived bool
		)

		if err = rows.Scan(&id, &name, &orgID, &archived); err != nil {
			return nil, fmt.Errorf("scanning projects by member row: %w", err)
		}

		pwr := repository.ProjectWithRole{ID: id, Name: name, Role: model.RoleUnspecified, OrgID: uuid.Nil, Archived: archived}
		if orgID != nil {
			pwr.OrgID = *orgID
		}

		results = append(results, pwr)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating projects by member: %w", err)
	}

	return results, nil
}
