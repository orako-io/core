// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/identity"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestOffboardDeletesMembershipRowsScopedToOrg proves the offboard access-residue
// fix: transitioning a member to a terminal status (removed AND, separately,
// purged) also deletes THAT member's project_members rows and the account's
// org_members authority row for THAT member's org — while leaving the SAME
// account's membership in a DIFFERENT org untouched. The members row itself is
// KEPT (status removed/purged) for answer attribution. An offboarded account no
// longer resolves the org (OrganizationsByAccount / ProjectsByMember drop it).
func TestOffboardDeletesMembershipRowsScopedToOrg(t *testing.T) {
	t.Parallel()

	for _, terminal := range []model.MemberStatus{model.MemberStatusRemoved, model.MemberStatusPurged} {
		t.Run(string(terminal), func(t *testing.T) {
			t.Parallel()

			pool := testsupport.RequirePostgres(t)
			memberStore := identity.NewMemberStore(pool)
			orgStore := identity.NewOrganizationStore(pool)
			projectStore := identity.NewProjectStore(pool)
			seeder := newOrgSeeder(pool)

			accountID := testsupport.SeedAccountForOrg(t, pool)

			// ── Org A: the account is admin + creator member. This is the org the
			// member will be offboarded from. ────────────────────────────────────
			orgA, memberA := seedOrgForAccount(t, seeder, accountID, "Org A")
			addToProject(t, pool, testsupport.SeedProjectInOrg(t, pool, orgA), memberA.ID)

			// ── Org B: SAME account, a separate member row. Must survive untouched.
			orgB, memberB := seedOrgForAccount(t, seeder, accountID, "Org B")

			// Both orgs are visible in the switcher before offboarding.
			if orgs := orgIDs(t, accountID, orgStore); !slices.Contains(orgs, orgA) || !slices.Contains(orgs, orgB) {
				t.Fatalf("pre-offboard: switcher = %v, want both org A %s and org B %s", orgs, orgA, orgB)
			}

			// ── Offboard the org-A member. Mirror the RemoveMemberHandler shape:
			// a purge blanks PII; a soft-remove keeps it. ─────────────────────────
			memberA.Status = terminal
			if terminal == model.MemberStatusPurged {
				memberA.Email = ""
				memberA.DisplayName = ""
			}

			if err := memberStore.OffboardFromOrg(t.Context(), memberA, orgA); err != nil {
				t.Fatalf("Offboard: %v", err)
			}

			// The member row is KEPT with the terminal status (answer attribution).
			got, err := memberStore.ByID(t.Context(), memberA.ID)
			if err != nil {
				t.Fatalf("ByID after offboard: %v (the members row must be kept)", err)
			}

			if got.Status != terminal {
				t.Errorf("member status = %q, want %q", got.Status, terminal)
			}

			// Org-A membership rows are gone.
			if n := countProjectMembers(t, pool, memberA.ID); n != 0 {
				t.Errorf("org-A project_members for offboarded member = %d, want 0", n)
			}

			if n := countOrgMembers(t, pool, accountID, orgA); n != 0 {
				t.Errorf("org-A org_members for account = %d, want 0 (authority revoked)", n)
			}

			// Org-B membership is untouched (different org, same account).
			if n := countProjectMembers(t, pool, memberB.ID); n != 1 {
				t.Errorf("org-B project_members = %d, want 1 (untouched)", n)
			}

			if n := countOrgMembers(t, pool, accountID, orgB); n != 1 {
				t.Errorf("org-B org_members = %d, want 1 (untouched)", n)
			}

			// The offboarded member no longer resolves org A.
			if pwrs, err := projectStore.ProjectsByMember(t.Context(), memberA.ID); err != nil {
				t.Fatalf("ProjectsByMember: %v", err)
			} else if len(pwrs) != 0 {
				t.Errorf("ProjectsByMember(offboarded) = %v, want empty", pwrs)
			}

			// The switcher now shows only org B for the account.
			orgs := orgIDs(t, accountID, orgStore)
			if slices.Contains(orgs, orgA) {
				t.Errorf("post-offboard: switcher still includes org A %s: %v", orgA, orgs)
			}

			if !slices.Contains(orgs, orgB) {
				t.Errorf("post-offboard: switcher lost org B %s: %v", orgB, orgs)
			}
		})
	}
}

func TestOffboardSharedMemberPreservesOtherOrganization(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	memberStore := identity.NewMemberStore(pool)
	seeder := newOrgSeeder(pool)
	accountID := testsupport.SeedAccountForOrg(t, pool)
	orgA, member := seedOrgForAccount(t, seeder, accountID, "Org A")
	orgB := testsupport.SeedOrganization(t, pool)
	projectB := testsupport.SeedProjectInOrg(t, pool, orgB)
	originalEmail := member.Email

	addToProject(t, pool, projectB, member.ID)
	if _, err := pool.Exec(t.Context(),
		"INSERT INTO org_members (org_id, account_id, role) VALUES ($1, $2, 'member')",
		orgB, accountID,
	); err != nil {
		t.Fatalf("adding org-B authority: %v", err)
	}

	member.Status = model.MemberStatusPurged
	member.Email = ""
	member.DisplayName = ""
	if err := memberStore.OffboardFromOrg(t.Context(), member, orgA); err != nil {
		t.Fatalf("OffboardFromOrg: %v", err)
	}

	got, err := memberStore.ByID(t.Context(), member.ID)
	if err != nil {
		t.Fatalf("ByID after scoped offboard: %v", err)
	}

	if got.Status != model.MemberStatusActive {
		t.Errorf("shared member status = %q, want active for org B", got.Status)
	}

	if got.Email != originalEmail {
		t.Errorf("shared member email = %q, want preserved %q", got.Email, originalEmail)
	}

	if n := countProjectMembersInOrg(t, pool, member.ID, orgA); n != 0 {
		t.Errorf("org-A project memberships = %d, want 0", n)
	}

	if n := countProjectMembersInOrg(t, pool, member.ID, orgB); n != 1 {
		t.Errorf("org-B project memberships = %d, want 1", n)
	}

	if n := countOrgMembers(t, pool, accountID, orgA); n != 0 {
		t.Errorf("org-A authority rows = %d, want 0", n)
	}

	if n := countOrgMembers(t, pool, accountID, orgB); n != 1 {
		t.Errorf("org-B authority rows = %d, want 1", n)
	}
}

// TestByAccountInOrgScopesToOrg proves the org-scoped membership lookup that backs
// the redeem idempotency guard: an account that is a live member of org A resolves
// its member for org A but is ErrNotFound for org B — so a join code for org B is
// not wrongly rejected as already-a-member.
func TestByAccountInOrgScopesToOrg(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	memberStore := identity.NewMemberStore(pool)
	seeder := newOrgSeeder(pool)

	accountID := testsupport.SeedAccountForOrg(t, pool)

	orgA, memberA := seedOrgForAccount(t, seeder, accountID, "Scope Org A")
	orgB := testsupport.SeedOrganization(t, pool) // an org the account has no member in

	// In org A the account resolves to its live member.
	got, err := memberStore.ByAccountInOrg(t.Context(), accountID, orgA)
	if err != nil {
		t.Fatalf("ByAccountInOrg(org A): %v", err)
	}

	if got.ID != memberA.ID {
		t.Errorf("ByAccountInOrg(org A) = %s, want the org-A member %s", got.ID, memberA.ID)
	}

	// In org B the account has no member: ErrNotFound (not a false already-member).
	if _, err := memberStore.ByAccountInOrg(t.Context(), accountID, orgB); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("ByAccountInOrg(org B) = %v, want ErrNotFound", err)
	}
}

// seedOrgForAccount stands up an org with a global project and an active creator
// member linked to accountID (giving the account one org_members row and one
// project_members row in the new org). Returns the org, project, and member.
func seedOrgForAccount(t *testing.T, seeder orgSeeder, accountID uuid.UUID, name string) (uuid.UUID, model.Member) {
	t.Helper()

	org, err := model.NewOrganization(uuid.New(), name)
	if err != nil {
		t.Fatalf("NewOrganization(%q): %v", name, err)
	}

	project, err := model.NewProjectInOrg(uuid.New(), "global", org.ID)
	if err != nil {
		t.Fatalf("NewProjectInOrg(%q): %v", name, err)
	}

	member, err := model.NewActiveMember(uuid.New(), accountID)
	if err != nil {
		t.Fatalf("NewActiveMember(%q): %v", name, err)
	}

	if err := seeder.CreateOrganizationWithGlobalProject(t.Context(), org, accountID, project, member); err != nil {
		t.Fatalf("CreateOrganizationWithGlobalProject(%q): %v", name, err)
	}

	return org.ID, member
}

func countProjectMembers(t *testing.T, pool *pgxpool.Pool, memberID uuid.UUID) int {
	t.Helper()

	var n int
	if err := pool.QueryRow(t.Context(),
		"SELECT COUNT(*) FROM project_members WHERE member_id = $1", memberID).Scan(&n); err != nil {
		t.Fatalf("counting project_members: %v", err)
	}

	return n
}

func countProjectMembersInOrg(t *testing.T, pool *pgxpool.Pool, memberID, orgID uuid.UUID) int {
	t.Helper()

	var n int
	if err := pool.QueryRow(t.Context(), `
		SELECT COUNT(*)
		FROM project_members pm
		JOIN projects p ON p.id = pm.project_id
		WHERE pm.member_id = $1 AND p.org_id = $2
	`, memberID, orgID).Scan(&n); err != nil {
		t.Fatalf("counting project_members in org: %v", err)
	}

	return n
}

func countOrgMembers(t *testing.T, pool *pgxpool.Pool, accountID, orgID uuid.UUID) int {
	t.Helper()

	var n int
	if err := pool.QueryRow(t.Context(),
		"SELECT COUNT(*) FROM org_members WHERE account_id = $1 AND org_id = $2", accountID, orgID).Scan(&n); err != nil {
		t.Fatalf("counting org_members: %v", err)
	}

	return n
}

func orgIDs(t *testing.T, accountID uuid.UUID, orgStore *identity.OrganizationStore) []uuid.UUID {
	t.Helper()

	summaries, err := orgStore.OrganizationsByAccount(t.Context(), accountID)
	if err != nil {
		t.Fatalf("OrganizationsByAccount: %v", err)
	}

	ids := make([]uuid.UUID, 0, len(summaries))
	for _, s := range summaries {
		ids = append(ids, s.ID)
	}

	return ids
}
