// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/orako-io/core/internal/adapters/identity"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// seatTestMember inserts a member (optionally account-linked) and returns its
// id. accountID is uuid.Nil for an external-only responder.
func seatTestMember(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, status string) uuid.UUID {
	t.Helper()

	id := uuid.New()

	var acct any
	if accountID != uuid.Nil {
		acct = accountID
	}

	if _, err := pool.Exec(t.Context(),
		"INSERT INTO members (id, email, status, account_id) VALUES ($1, $2, $3, $4)",
		id, uuid.NewString()+"@spec.test", status, acct,
	); err != nil {
		t.Fatalf("seatTestMember: %v", err)
	}

	return id
}

func addToProject(t *testing.T, pool *pgxpool.Pool, projectID, memberID uuid.UUID) {
	t.Helper()

	if _, err := pool.Exec(t.Context(),
		"INSERT INTO project_members (project_id, member_id) VALUES ($1, $2)",
		projectID, memberID,
	); err != nil {
		t.Fatalf("addToProject: %v", err)
	}
}

// TestActiveMemberCountByOrg proves the seat rule: one distinct person per seat.
// An account (org_member) counts once; an external-only responder in several of
// the org's projects counts once (deduped by member id); removed/purged members
// do not count. Member counting is an identity concern (OrganizationStore),
// independent of billing.
func TestActiveMemberCountByOrg(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewOrganizationStore(pool)

	orgID := testsupport.SeedOrganization(t, pool)
	projectA := testsupport.SeedProjectInOrg(t, pool, orgID)
	projectB := testsupport.SeedProjectInOrg(t, pool, orgID)

	// 1 admin account in org_members, also a member of both projects (account_id
	// set → counted via org_members, not the external branch).
	adminAccount := testsupport.SeedAccountForOrg(t, pool)
	if _, err := pool.Exec(t.Context(),
		"INSERT INTO org_members (org_id, account_id, role) VALUES ($1, $2, 'admin')",
		orgID, adminAccount,
	); err != nil {
		t.Fatalf("insert org_member: %v", err)
	}

	adminMember := seatTestMember(t, pool, adminAccount, "active")
	addToProject(t, pool, projectA, adminMember)
	addToProject(t, pool, projectB, adminMember)

	// 1 external-only responder in BOTH projects → still one seat.
	responder := seatTestMember(t, pool, uuid.Nil, "active")
	addToProject(t, pool, projectA, responder)
	addToProject(t, pool, projectB, responder)

	// 1 external-only responder in one project → one seat.
	loner := seatTestMember(t, pool, uuid.Nil, "active")
	addToProject(t, pool, projectA, loner)

	// A removed external member does not count.
	removed := seatTestMember(t, pool, uuid.Nil, "removed")
	addToProject(t, pool, projectB, removed)

	got, err := store.CountByOrg(t.Context(), orgID)
	if err != nil {
		t.Fatalf("CountByOrg: %v", err)
	}

	// admin account (1) + external responder (1) + loner (1) = 3.
	if got != 3 {
		t.Errorf("CountByOrg = %d, want 3", got)
	}
}

// TestActiveMemberCountByOrg_ExcludesArchivedOnlyExternalMember proves an
// external-only member whose ONLY project is archived does not consume a seat,
// while one with at least one non-archived project still counts even if they
// also sit in an archived one.
func TestActiveMemberCountByOrg_ExcludesArchivedOnlyExternalMember(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewOrganizationStore(pool)
	projectStore := identity.NewProjectStore(pool)

	orgID := testsupport.SeedOrganization(t, pool)
	activeProject := testsupport.SeedProjectInOrg(t, pool, orgID)
	archivedProject := testsupport.SeedProjectInOrg(t, pool, orgID)

	if err := projectStore.SetArchived(t.Context(), archivedProject, true); err != nil {
		t.Fatalf("SetArchived: %v", err)
	}

	// External-only member whose ONLY project is archived: must not count.
	archivedOnly := seatTestMember(t, pool, uuid.Nil, "active")
	addToProject(t, pool, archivedProject, archivedOnly)

	// External-only member in both an active and the archived project: still
	// counts (they have a live seat elsewhere).
	both := seatTestMember(t, pool, uuid.Nil, "active")
	addToProject(t, pool, activeProject, both)
	addToProject(t, pool, archivedProject, both)

	got, err := store.CountByOrg(t.Context(), orgID)
	if err != nil {
		t.Fatalf("CountByOrg: %v", err)
	}

	if got != 1 {
		t.Errorf("CountByOrg = %d, want 1 (archived-only member excluded)", got)
	}

	// Reactivating the project restores the archived-only member's seat.
	if err := projectStore.SetArchived(t.Context(), archivedProject, false); err != nil {
		t.Fatalf("SetArchived(reactivate): %v", err)
	}

	got, err = store.CountByOrg(t.Context(), orgID)
	if err != nil {
		t.Fatalf("CountByOrg (after reactivate): %v", err)
	}

	if got != 2 {
		t.Errorf("CountByOrg after reactivate = %d, want 2", got)
	}
}
