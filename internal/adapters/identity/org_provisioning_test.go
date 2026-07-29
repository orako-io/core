// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestOrgProvisioningWritesAllRows proves that all five rows are written in a
// single transaction: organization, org_members (admin), projects (org_id set),
// members (account_id + status=active), project_members (admin role).
func TestOrgProvisioningWritesAllRows(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := newOrgSeeder(pool)

	accountID := testsupport.SeedAccountForOrg(t, pool)

	orgID := uuid.New()

	org, err := model.NewOrganization(orgID, "Acme Corp")
	if err != nil {
		t.Fatalf("NewOrganization: %v", err)
	}

	globalProjectID := uuid.New()

	globalProject, err := model.NewProjectInOrg(globalProjectID, "global", orgID)
	if err != nil {
		t.Fatalf("NewProjectInOrg: %v", err)
	}

	memberID := uuid.New()

	creatorMember, err := model.NewActiveMember(memberID, accountID)
	if err != nil {
		t.Fatalf("NewActiveMember: %v", err)
	}

	if err := store.CreateOrganizationWithGlobalProject(
		t.Context(), org, accountID, globalProject, creatorMember,
	); err != nil {
		t.Fatalf("CreateOrganizationWithGlobalProject: %v", err)
	}

	// ── Assert exactly one row per table ─────────────────────────────────────

	// 1. organizations
	var orgCount int
	if err := pool.QueryRow(t.Context(),
		"SELECT COUNT(*) FROM organizations WHERE id = $1 AND name = $2",
		orgID, "Acme Corp",
	).Scan(&orgCount); err != nil {
		t.Fatalf("query organizations: %v", err)
	}

	if orgCount != 1 {
		t.Errorf("organizations: got %d rows, want 1", orgCount)
	}

	// 2. org_members (admin role)
	var orgMemberCount int
	if err := pool.QueryRow(t.Context(),
		"SELECT COUNT(*) FROM org_members WHERE org_id = $1 AND account_id = $2 AND role = 'admin'",
		orgID, accountID,
	).Scan(&orgMemberCount); err != nil {
		t.Fatalf("query org_members: %v", err)
	}

	if orgMemberCount != 1 {
		t.Errorf("org_members: got %d rows, want 1", orgMemberCount)
	}

	// 3. projects (org_id set, name = global)
	var projectCount int
	if err := pool.QueryRow(t.Context(),
		"SELECT COUNT(*) FROM projects WHERE id = $1 AND org_id = $2 AND name = 'global'",
		globalProjectID, orgID,
	).Scan(&projectCount); err != nil {
		t.Fatalf("query projects: %v", err)
	}

	if projectCount != 1 {
		t.Errorf("projects: got %d rows, want 1", projectCount)
	}

	// 4. members (account_id set, status = active)
	var memberCount int
	if err := pool.QueryRow(t.Context(),
		"SELECT COUNT(*) FROM members WHERE id = $1 AND account_id = $2 AND status = 'active'",
		memberID, accountID,
	).Scan(&memberCount); err != nil {
		t.Fatalf("query members: %v", err)
	}

	if memberCount != 1 {
		t.Errorf("members: got %d rows, want 1", memberCount)
	}

	// 5. project_members — one role-less row for the creator; admin authority is
	//    the org_members(admin) write, not this membership.
	var pmCount int
	if err := pool.QueryRow(t.Context(),
		"SELECT COUNT(*) FROM project_members WHERE project_id = $1 AND member_id = $2",
		globalProjectID, memberID,
	).Scan(&pmCount); err != nil {
		t.Fatalf("query project_members: %v", err)
	}

	if pmCount != 1 {
		t.Errorf("project_members: got %d rows, want 1", pmCount)
	}
}

// TestOrgProvisioningRollsBack proves that a mid-transaction failure
// leaves zero rows in all five tables. The failure is forced by providing a
// non-existent adminAccountID: step 2 (addOrgMember) fails on the FK constraint
// against accounts(id) after step 1 (createOrganization) has already run in the
// tx. The full rollback must also undo step 1.
func TestOrgProvisioningRollsBack(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := newOrgSeeder(pool)

	// Intentionally NOT seeding an account — forces step 2 (addOrgMember) to fail
	// with a FK violation. Step 1 (createOrganization) has already executed in the
	// tx and must be rolled back together with it.
	nonExistentAccountID := uuid.New()

	orgID := uuid.New()
	org, _ := model.NewOrganization(orgID, "Rollback Corp")

	globalProjectID := uuid.New()
	globalProject, _ := model.NewProjectInOrg(globalProjectID, "global", orgID)

	memberID := uuid.New()
	creatorMember, _ := model.NewActiveMember(memberID, nonExistentAccountID)

	err := store.CreateOrganizationWithGlobalProject(
		t.Context(), org, nonExistentAccountID, globalProject, creatorMember,
	)
	if err == nil {
		t.Fatal("CreateOrganizationWithGlobalProject: want error for missing account, got nil")
	}

	// ── Assert zero rows in all five tables ───────────────────────────────────

	// 1. organizations — step 1 rolled back.
	var orgCount int
	if scanErr := pool.QueryRow(t.Context(),
		"SELECT COUNT(*) FROM organizations WHERE id = $1", orgID,
	).Scan(&orgCount); scanErr != nil {
		t.Fatalf("query organizations: %v", scanErr)
	}

	if orgCount != 0 {
		t.Errorf("organizations: got %d rows after rollback, want 0", orgCount)
	}

	// 2. org_members — step 2 failed (the failure that triggered rollback).
	var orgMemberCount int
	if scanErr := pool.QueryRow(t.Context(),
		"SELECT COUNT(*) FROM org_members WHERE org_id = $1", orgID,
	).Scan(&orgMemberCount); scanErr != nil {
		t.Fatalf("query org_members: %v", scanErr)
	}

	if orgMemberCount != 0 {
		t.Errorf("org_members: got %d rows after rollback, want 0", orgMemberCount)
	}

	// 3. projects — step 3 never ran.
	var projectCount int
	if scanErr := pool.QueryRow(t.Context(),
		"SELECT COUNT(*) FROM projects WHERE id = $1", globalProjectID,
	).Scan(&projectCount); scanErr != nil {
		t.Fatalf("query projects: %v", scanErr)
	}

	if projectCount != 0 {
		t.Errorf("projects: got %d rows after rollback, want 0", projectCount)
	}

	// 4. members — step 4 never ran.
	var memberCount int
	if scanErr := pool.QueryRow(t.Context(),
		"SELECT COUNT(*) FROM members WHERE id = $1", memberID,
	).Scan(&memberCount); scanErr != nil {
		t.Fatalf("query members: %v", scanErr)
	}

	if memberCount != 0 {
		t.Errorf("members: got %d rows after rollback, want 0", memberCount)
	}

	// 5. project_members — step 5 never ran.
	var pmCount int
	if scanErr := pool.QueryRow(t.Context(),
		"SELECT COUNT(*) FROM project_members WHERE project_id = $1", globalProjectID,
	).Scan(&pmCount); scanErr != nil {
		t.Fatalf("query project_members: %v", scanErr)
	}

	if pmCount != 0 {
		t.Errorf("project_members: got %d rows after rollback, want 0", pmCount)
	}
}
