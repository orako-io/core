// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/identity"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestOrganizationStore_AccountKeyedMultiOrg reproduces the "creating an
// organization from the dashboard does nothing" bug (2026-07-13): each org
// creates its OWN member row for the account, so the member-keyed reads only
// ever saw the token member's org — a newly created second org was invisible
// in the switcher (OrganizationsByMember) and unreachable by the Orako-Org-Id
// header (ProjectInOrgForMember). The account-keyed reads fix both.
func TestOrganizationStore_AccountKeyedMultiOrg(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	orgAtomic := newOrgSeeder(pool)
	orgStore := identity.NewOrganizationStore(pool)

	accountID := testsupport.SeedAccountForOrg(t, pool)

	createOrg := func(name string) (orgID, memberID uuid.UUID) {
		t.Helper()

		org, err := model.NewOrganization(uuid.New(), name)
		if err != nil {
			t.Fatalf("NewOrganization: %v", err)
		}

		project, err := model.NewProjectInOrg(uuid.New(), "global", org.ID)
		if err != nil {
			t.Fatalf("NewProjectInOrg: %v", err)
		}

		member, err := model.NewActiveMember(uuid.New(), accountID)
		if err != nil {
			t.Fatalf("NewActiveMember: %v", err)
		}

		if err := orgAtomic.CreateOrganizationWithGlobalProject(
			t.Context(), org, accountID, project, member,
		); err != nil {
			t.Fatalf("CreateOrganizationWithGlobalProject(%s): %v", name, err)
		}

		return org.ID, member.ID
	}

	firstOrgID, firstMemberID := createOrg("Uxia Multiorg Test")
	secondOrgID, secondMemberID := createOrg("Viadi Multiorg Test")

	// The switcher must list BOTH orgs for the account, even though the token
	// resolves to only one member row.
	orgs, err := orgStore.OrganizationsByAccount(t.Context(), accountID)
	if err != nil {
		t.Fatalf("OrganizationsByAccount: %v", err)
	}

	seen := map[uuid.UUID]bool{}
	for _, o := range orgs {
		seen[o.ID] = true
	}

	if !seen[firstOrgID] || !seen[secondOrgID] {
		t.Fatalf("OrganizationsByAccount must list both orgs, got %v", orgs)
	}

	// The member-keyed legacy read only sees the member's own org — this is
	// exactly why the second org used to be invisible.
	legacy, err := orgStore.OrganizationsByMember(t.Context(), firstMemberID)
	if err != nil {
		t.Fatalf("OrganizationsByMember: %v", err)
	}

	if len(legacy) != 1 || legacy[0].ID != firstOrgID {
		t.Fatalf("member-keyed read should see exactly the member's own org, got %v", legacy)
	}

	// Switching to the second org must resolve the account's member row IN
	// that org (not the token's).
	projectID, memberID, ok, err := orgStore.ProjectInOrgForAccount(t.Context(), accountID, secondOrgID)
	if err != nil || !ok {
		t.Fatalf("ProjectInOrgForAccount(second org): ok=%v err=%v", ok, err)
	}

	if memberID != secondMemberID {
		t.Errorf("scoped member = %s, want the second org's member %s", memberID, secondMemberID)
	}

	if projectID == uuid.Nil {
		t.Error("scoped project must be the second org's global project")
	}

	// A stranger account has no foothold: ok=false, never an error.
	if _, _, ok, err := orgStore.ProjectInOrgForAccount(t.Context(), uuid.New(), secondOrgID); err != nil || ok {
		t.Fatalf("stranger account: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}
