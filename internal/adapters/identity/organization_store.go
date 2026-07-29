// SPDX-License-Identifier: AGPL-3.0-or-later

package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/query"
	postgres "github.com/orako-io/core/internal/pkg/postgres"
)

var _ repository.OrganizationRepository = (*OrganizationStore)(nil)

// OrganizationStore is the Postgres-backed OrganizationRepository.
type OrganizationStore struct {
	pool *pgxpool.Pool
}

// NewOrganizationStore builds an OrganizationStore backed by pool.
func NewOrganizationStore(pool *pgxpool.Pool) *OrganizationStore {
	return &OrganizationStore{pool: pool}
}

// Create durably stores a new organization.
func (s *OrganizationStore) Create(ctx context.Context, org model.Organization) error {
	_, err := New(postgres.Conn(ctx, s.pool)).createOrganization(ctx, createOrganizationParams{ID: org.ID, Name: org.Name})
	if err != nil {
		return fmt.Errorf("creating organization: %w", adaptererr.Decode(err))
	}

	return nil
}

// CountAll returns the number of organizations in the instance. Backs the
// Community-edition single-org cap.
func (s *OrganizationStore) CountAll(ctx context.Context) (int, error) {
	n, err := New(postgres.Conn(ctx, s.pool)).countOrganizations(ctx)
	if err != nil {
		return 0, fmt.Errorf("counting organizations: %w", adaptererr.Decode(err))
	}

	return int(n), nil
}

// CountInstanceSeats returns the instance-wide seat count (distinct billable
// persons across all orgs). Backs the Licensed-edition seat cap.
func (s *OrganizationStore) CountInstanceSeats(ctx context.Context) (int, error) {
	n, err := New(postgres.Conn(ctx, s.pool)).countInstanceSeats(ctx)
	if err != nil {
		return 0, fmt.Errorf("counting instance seats: %w", adaptererr.Decode(err))
	}

	return int(n), nil
}

// ByID fetches an organization by its UUID.
func (s *OrganizationStore) ByID(ctx context.Context, id uuid.UUID) (model.Organization, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).organizationByID(ctx, id)
	if err != nil {
		return model.Organization{}, fmt.Errorf("fetching organization by id: %w", adaptererr.Decode(err))
	}

	return model.Organization{ID: row.ID, Name: row.Name, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}

// CountByOrg returns the org's current active-member count (the "seat usage" the
// member gates enforce against). Counting members is an identity concern, not a
// billing one; the billing edition simply reads this number.
func (s *OrganizationStore) CountByOrg(ctx context.Context, orgID uuid.UUID) (int, error) {
	count, err := New(postgres.Conn(ctx, s.pool)).activeMemberCountByOrg(ctx, orgID)
	if err != nil {
		return 0, fmt.Errorf("counting active members: %w", adaptererr.Decode(err))
	}

	return int(count), nil
}

// ReadOrgIdentity returns an organization's identity (id + name) as a query read
// model, so the read side never depends on the domain aggregate.
func (s *OrganizationStore) ReadOrgIdentity(ctx context.Context, id uuid.UUID) (query.OrgIdentityView, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).organizationByID(ctx, id)
	if err != nil {
		return query.OrgIdentityView{}, fmt.Errorf("fetching organization by id: %w", adaptererr.Decode(err))
	}

	return query.OrgIdentityView{ID: row.ID, Name: row.Name}, nil
}

// Rename updates an organization's name in place (updated_at is bumped by the
// query). Returns adaptererr.ErrNotFound when the org is absent.
func (s *OrganizationStore) Rename(ctx context.Context, id uuid.UUID, name string) error {
	_, err := New(postgres.Conn(ctx, s.pool)).updateOrganization(ctx, updateOrganizationParams{ID: id, Name: name})
	if err != nil {
		return fmt.Errorf("renaming organization: %w", adaptererr.Decode(err))
	}

	return nil
}

// DeleteOrganization hard-deletes an organization and all of its data. It runs
// through postgres.Conn so it joins the caller's ambient transaction (the
// command handler opens one), keeping the whole cascade atomic.
//
// Almost everything cascades off two FKs: organizations (org_members,
// org_providers, org_join_tokens, and projects via projects.org_id) and, in
// turn, projects (project_members, conversations→messages/candidates/
// participants/surfaces/provider_messages/attachments, kb_entries→kb_feedback,
// event_log) — all declared ON DELETE CASCADE in the migrations. The one
// exception is members: they carry no org FK (only a nullable account link), so
// deleting the org would orphan them. We capture the org's members before the
// cascade drops their project_members links, then delete those that end up with
// no membership anywhere — i.e. the members that belonged only to this org.
// A member still referenced by another org's projects is left untouched.
func (s *OrganizationStore) DeleteOrganization(ctx context.Context, id uuid.UUID) error {
	db := postgres.Conn(ctx, s.pool)

	rows, err := db.Query(ctx, `
		SELECT DISTINCT pm.member_id
		FROM project_members pm
		JOIN projects p ON p.id = pm.project_id
		WHERE p.org_id = $1`, id)
	if err != nil {
		return fmt.Errorf("collecting org members: %w", adaptererr.Decode(err))
	}

	var memberIDs []uuid.UUID

	for rows.Next() {
		var memberID uuid.UUID
		if err := rows.Scan(&memberID); err != nil {
			rows.Close()

			return fmt.Errorf("scanning org member id: %w", adaptererr.Decode(err))
		}

		memberIDs = append(memberIDs, memberID)
	}

	rows.Close()

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating org member ids: %w", adaptererr.Decode(err))
	}

	// Delete the org: FK ON DELETE CASCADE removes projects and everything under
	// them, plus org_members / org_providers / org_join_tokens.
	tag, err := db.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting organization: %w", adaptererr.Decode(err))
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("deleting organization: %w", adaptererr.ErrNotFound)
	}

	// Remove members that belonged only to this org (now orphaned: the cascade
	// dropped their only project_members rows). ANY($1) is a no-op for an empty
	// slice, but skip the round-trip when there were none.
	if len(memberIDs) > 0 {
		if _, err := db.Exec(ctx, `
			DELETE FROM members m
			WHERE m.id = ANY($1)
			  AND NOT EXISTS (SELECT 1 FROM project_members pm WHERE pm.member_id = m.id)`, memberIDs); err != nil {
			return fmt.Errorf("deleting orphaned org members: %w", adaptererr.Decode(err))
		}
	}

	return nil
}

// AddMember adds or updates an account's role in an org.
func (s *OrganizationStore) AddMember(ctx context.Context, m model.OrgMembership) error {
	err := New(postgres.Conn(ctx, s.pool)).addOrgMember(ctx, addOrgMemberParams{
		OrgID:     m.OrgID,
		AccountID: m.AccountID,
		Role:      string(m.Role),
	})
	if err != nil {
		return fmt.Errorf("adding org member: %w", adaptererr.Decode(err))
	}

	return nil
}

// RoleFor returns the account's role in the org.
func (s *OrganizationStore) RoleFor(ctx context.Context, orgID, accountID uuid.UUID) (model.OrgRole, error) {
	role, err := New(postgres.Conn(ctx, s.pool)).orgMemberRole(ctx, orgMemberRoleParams{OrgID: orgID, AccountID: accountID})
	if err != nil {
		return "", fmt.Errorf("fetching org member role: %w", adaptererr.Decode(err))
	}

	return model.OrgRole(role), nil
}

// OrganizationsByMember lists the orgs the member participates in via projects.
func (s *OrganizationStore) OrganizationsByMember(ctx context.Context, memberID uuid.UUID) ([]query.OrgSummary, error) {
	return s.queryOrgSummaries(ctx, `
		SELECT DISTINCT o.id, o.name
		FROM organizations o
		JOIN projects p ON p.org_id = o.id
		JOIN project_members pm ON pm.project_id = p.id
		WHERE pm.member_id = $1
		ORDER BY o.name`, memberID)
}

// OrganizationsByAccount lists every org the account participates in — via
// any of its member rows' project memberships (each org has its OWN member
// row per account) or via an org_members row (e.g. the admin of a
// freshly-created org). This is the multi-org switcher's source of truth;
// keying it on a single member row hid every org but the token's default one.
func (s *OrganizationStore) OrganizationsByAccount(ctx context.Context, accountID uuid.UUID) ([]query.OrgSummary, error) {
	return s.queryOrgSummaries(ctx, `
		SELECT DISTINCT o.id, o.name
		FROM organizations o
		WHERE o.id IN (
			SELECT p.org_id
			FROM projects p
			JOIN project_members pm ON pm.project_id = p.id
			JOIN members m ON m.id = pm.member_id
			WHERE m.account_id = $1
		)
		OR o.id IN (SELECT org_id FROM org_members WHERE account_id = $1)
		ORDER BY o.name`, accountID)
}

// queryOrgSummaries runs an (id, name) org query and scans the summaries.
func (s *OrganizationStore) queryOrgSummaries(ctx context.Context, sql string, arg uuid.UUID) ([]query.OrgSummary, error) {
	rows, err := s.pool.Query(ctx, sql, arg)
	if err != nil {
		return nil, fmt.Errorf("listing organizations: %w", adaptererr.Decode(err))
	}
	defer rows.Close()

	var orgs []query.OrgSummary

	for rows.Next() {
		var o query.OrgSummary
		if err := rows.Scan(&o.ID, &o.Name); err != nil {
			return nil, fmt.Errorf("scanning organization row: %w", adaptererr.Decode(err))
		}

		orgs = append(orgs, o)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating organization rows: %w", adaptererr.Decode(err))
	}

	return orgs, nil
}

// ProjectInOrgForAccount resolves the account's own member row and one of its
// projects INSIDE orgID — the account-keyed org switch: each org has its own
// member row per account, so the token's member id is meaningless in another
// org.
func (s *OrganizationStore) ProjectInOrgForAccount(ctx context.Context, accountID, orgID uuid.UUID) (projectID, memberID uuid.UUID, ok bool, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT p.id, m.id
		FROM projects p
		JOIN project_members pm ON pm.project_id = p.id
		JOIN members m ON m.id = pm.member_id
		WHERE m.account_id = $1 AND p.org_id = $2
		ORDER BY p.created_at ASC
		LIMIT 1`, accountID, orgID).Scan(&projectID, &memberID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, false, nil
	}

	if err != nil {
		return uuid.Nil, uuid.Nil, false, fmt.Errorf("resolving account project in org: %w", adaptererr.Decode(err))
	}

	return projectID, memberID, true, nil
}

// ProjectInOrgForMember returns one project the member belongs to in orgID.
func (s *OrganizationStore) ProjectInOrgForMember(ctx context.Context, memberID, orgID uuid.UUID) (uuid.UUID, bool, error) {
	var projectID uuid.UUID

	err := s.pool.QueryRow(ctx, `
		SELECT p.id
		FROM projects p
		JOIN project_members pm ON pm.project_id = p.id
		WHERE pm.member_id = $1 AND p.org_id = $2
		ORDER BY p.created_at ASC
		LIMIT 1`, memberID, orgID).Scan(&projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}

	if err != nil {
		return uuid.Nil, false, fmt.Errorf("resolving member project in org: %w", adaptererr.Decode(err))
	}

	return projectID, true, nil
}

// AdminCount returns how many admins the org currently has (for the last-admin guard).
func (s *OrganizationStore) AdminCount(ctx context.Context, orgID uuid.UUID) (int, error) {
	n, err := New(postgres.Conn(ctx, s.pool)).countOrgAdmins(ctx, orgID)
	if err != nil {
		return 0, fmt.Errorf("counting org admins: %w", adaptererr.Decode(err))
	}

	return int(n), nil
}
