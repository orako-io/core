// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/identity"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestProjectStoreCreateAndByID proves the create-then-fetch round trip.
func TestProjectStoreCreateAndByID(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewProjectStore(pool)

	project, err := model.NewProjectInOrg(uuid.New(), "Orako MVP", uuid.New())
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}

	if err := store.Create(t.Context(), project); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.ByID(t.Context(), project.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}

	if got.ID != project.ID {
		t.Errorf("ID: got %v, want %v", got.ID, project.ID)
	}

	if got.Name != "Orako MVP" {
		t.Errorf("Name: got %q, want %q", got.Name, "Orako MVP")
	}
}

// TestProjectStoreByIDNotFound proves ByID returns ErrNotFound for an absent project.
func TestProjectStoreByIDNotFound(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewProjectStore(pool)

	_, err := store.ByID(t.Context(), uuid.New())
	if !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("ByID(absent): got %v, want ErrNotFound", err)
	}
}

// TestProjectStoreDuplicate proves creating a project with the same ID returns ErrDuplicate.
func TestProjectStoreDuplicate(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewProjectStore(pool)

	project, _ := model.NewProjectInOrg(uuid.New(), "Dup Project", uuid.New())

	if err := store.Create(t.Context(), project); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	err := store.Create(t.Context(), project)
	if !errors.Is(err, adaptererr.ErrDuplicate) {
		t.Fatalf("second Create: got %v, want ErrDuplicate", err)
	}
}

// TestProjectStoreAddMemberAndList proves AddMember records a membership and
// MembersByProject returns it with the correct role and domains.
func TestProjectStoreAddMemberAndList(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewProjectStore(pool)

	projectID := testsupport.SeedProject(t, pool)
	memberID := testsupport.SeedMember(t, pool)

	membership := repository.ProjectMembership{
		ProjectID: projectID,
		MemberID:  memberID,
		Role:      model.RoleSpecialist,
		Domains:   []string{"auth", "infra"},
	}

	if err := store.AddMember(t.Context(), membership); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	list, err := store.MembersByProject(t.Context(), projectID)
	if err != nil {
		t.Fatalf("MembersByProject: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("MembersByProject: got %d, want 1", len(list))
	}

	got := list[0]

	if got.MemberID != memberID {
		t.Errorf("MemberID: got %v, want %v", got.MemberID, memberID)
	}

	// Role is no longer persisted on a project membership (migration 0007).
	if got.Role != model.RoleUnspecified {
		t.Errorf("Role: got %v, want Unspecified (project role retired)", got.Role)
	}

	if len(got.Domains) != 2 || got.Domains[0] != "auth" {
		t.Errorf("Domains: got %v", got.Domains)
	}
}

// TestProjectStoreAddMemberDuplicate proves that after migration 0007 the primary
// key is (project_id, member_id): adding the same member to the same project
// twice returns ErrDuplicate, and a second add with a different role (which is no
// longer part of the key, and not persisted) also conflicts.
func TestProjectStoreAddMemberDuplicate(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewProjectStore(pool)

	projectID := testsupport.SeedProject(t, pool)
	memberID := testsupport.SeedMember(t, pool)

	m := repository.ProjectMembership{
		ProjectID: projectID,
		MemberID:  memberID,
		Role:      model.RoleDev,
		Domains:   []string{},
	}

	if err := store.AddMember(t.Context(), m); err != nil {
		t.Fatalf("first AddMember: %v", err)
	}

	// Different role, same (project, member): still a PK conflict now.
	m.Role = model.RoleSpecialist
	if err := store.AddMember(t.Context(), m); !errors.Is(err, adaptererr.ErrDuplicate) {
		t.Fatalf("second AddMember: got %v, want ErrDuplicate", err)
	}
}

// TestProjectStoreSetMemberDomains proves the set-expertise-tags write repurposed
// from the retired role-assignment path: SetMemberDomains UPDATEs an existing
// membership's domains (replace, not append), is idempotent for an enrolled
// member (no ErrDuplicate — the Part 1 regression), and returns ErrNotFound for a
// member that is not enrolled.
func TestProjectStoreSetMemberDomains(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewProjectStore(pool)

	projectID := testsupport.SeedProject(t, pool)
	memberID := testsupport.SeedMember(t, pool)

	if err := store.AddMember(t.Context(), repository.ProjectMembership{
		ProjectID: projectID,
		MemberID:  memberID,
		Role:      model.RoleUnspecified,
		Domains:   []string{"auth"},
	}); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	// Replace the tags.
	if err := store.SetMemberDomains(t.Context(), projectID, memberID, []string{"backend", "cto"}); err != nil {
		t.Fatalf("SetMemberDomains: %v", err)
	}

	list, err := store.MembersByProject(t.Context(), projectID)
	if err != nil {
		t.Fatalf("MembersByProject: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("MembersByProject: got %d, want 1", len(list))
	}

	if got := list[0].Domains; len(got) != 2 || got[0] != "backend" || got[1] != "cto" {
		t.Errorf("Domains after set: got %v, want [backend cto] (replace, not append)", got)
	}

	// Idempotent: setting the same tags again succeeds (no unique violation).
	if err := store.SetMemberDomains(t.Context(), projectID, memberID, []string{"backend", "cto"}); err != nil {
		t.Fatalf("SetMemberDomains (idempotent): %v", err)
	}

	// Not enrolled → ErrNotFound.
	if err := store.SetMemberDomains(t.Context(), projectID, uuid.New(), []string{"x"}); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("SetMemberDomains (not enrolled): got %v, want ErrNotFound", err)
	}
}

func TestProjectStoreSetDomainsForMemberInOrg(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewProjectStore(pool)
	memberID := testsupport.SeedMember(t, pool)
	orgA := testsupport.SeedOrganization(t, pool)
	orgB := testsupport.SeedOrganization(t, pool)
	projectA := testsupport.SeedProjectInOrg(t, pool, orgA)
	projectB := testsupport.SeedProjectInOrg(t, pool, orgB)

	for _, projectID := range []uuid.UUID{projectA, projectB} {
		if err := store.AddMember(t.Context(), repository.ProjectMembership{
			ProjectID: projectID,
			MemberID:  memberID,
			Role:      model.RoleUnspecified,
			Domains:   []string{"old"},
		}); err != nil {
			t.Fatalf("AddMember(%s): %v", projectID, err)
		}
	}

	if err := store.SetDomainsForMemberInOrg(t.Context(), orgA, memberID, []string{"new"}); err != nil {
		t.Fatalf("SetDomainsForMemberInOrg: %v", err)
	}

	membersA, err := store.MembersByProject(t.Context(), projectA)
	if err != nil {
		t.Fatalf("MembersByProject(A): %v", err)
	}

	membersB, err := store.MembersByProject(t.Context(), projectB)
	if err != nil {
		t.Fatalf("MembersByProject(B): %v", err)
	}

	if got := membersA[0].Domains; len(got) != 1 || got[0] != "new" {
		t.Fatalf("org A domains = %v, want [new]", got)
	}

	if got := membersB[0].Domains; len(got) != 1 || got[0] != "old" {
		t.Fatalf("org B domains = %v, want [old]", got)
	}
}

// TestProjectStoreRename proves Rename persists the new name and returns
// ErrNotFound for an absent project.
func TestProjectStoreRename(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewProjectStore(pool)

	projectID := testsupport.SeedProject(t, pool)

	if err := store.Rename(t.Context(), projectID, "Renamed Project"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	got, err := store.ByID(t.Context(), projectID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}

	if got.Name != "Renamed Project" {
		t.Errorf("Name after rename: got %q, want %q", got.Name, "Renamed Project")
	}

	if err := store.Rename(t.Context(), uuid.New(), "x"); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("Rename(absent): got %v, want ErrNotFound", err)
	}
}

// TestProjectStoreSetArchived proves SetArchived sets and clears archived_at,
// and ByID/Archived() reflect it — the reversible-freeze round trip.
func TestProjectStoreSetArchived(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewProjectStore(pool)

	projectID := testsupport.SeedProject(t, pool)

	got, err := store.ByID(t.Context(), projectID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}

	if got.Archived() {
		t.Fatal("fresh project: Archived() = true, want false")
	}

	if err := store.SetArchived(t.Context(), projectID, true); err != nil {
		t.Fatalf("SetArchived(true): %v", err)
	}

	got, err = store.ByID(t.Context(), projectID)
	if err != nil {
		t.Fatalf("ByID after archive: %v", err)
	}

	if !got.Archived() {
		t.Error("after SetArchived(true): Archived() = false, want true")
	}

	if err := store.SetArchived(t.Context(), projectID, false); err != nil {
		t.Fatalf("SetArchived(false): %v", err)
	}

	got, err = store.ByID(t.Context(), projectID)
	if err != nil {
		t.Fatalf("ByID after reactivate: %v", err)
	}

	if got.Archived() {
		t.Error("after SetArchived(false): Archived() = true, want false (reactivated)")
	}

	if err := store.SetArchived(t.Context(), uuid.New(), true); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("SetArchived(absent): got %v, want ErrNotFound", err)
	}
}

// TestProjectStoreDelete proves Delete hard-deletes the project and every
// dependent row (project_members, conversations, messages, project_providers)
// via the projects FK's ON DELETE CASCADE.
func TestProjectStoreDelete(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewProjectStore(pool)

	projectID := testsupport.SeedProject(t, pool)
	memberID := testsupport.SeedMember(t, pool)

	if err := store.AddMember(t.Context(), repository.ProjectMembership{
		ProjectID: projectID,
		MemberID:  memberID,
		Role:      model.RoleUnspecified,
		Domains:   []string{},
	}); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	convID := uuid.New()

	if _, err := pool.Exec(
		t.Context(),
		`INSERT INTO conversations (id, project_id, asker_member_id, status, question)
		 VALUES ($1, $2, $3, 'open', 'q?')`,
		convID, projectID, memberID,
	); err != nil {
		t.Fatalf("seeding conversation: %v", err)
	}

	if _, err := pool.Exec(
		t.Context(),
		`INSERT INTO messages (id, conversation_id, author_member_id, role, body)
		 VALUES ($1, $2, $3, 'question', 'q?')`,
		uuid.New(), convID, memberID,
	); err != nil {
		t.Fatalf("seeding message: %v", err)
	}

	if _, err := pool.Exec(
		t.Context(),
		`INSERT INTO project_providers (id, project_id, kind, credentials, alert_channel_ids)
		 VALUES ($1, $2, 'slack', '{}'::jsonb, '{}')`,
		uuid.New(), projectID,
	); err != nil {
		t.Fatalf("seeding project_providers: %v", err)
	}

	if err := store.Delete(t.Context(), projectID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := store.ByID(t.Context(), projectID); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("ByID after delete: got %v, want ErrNotFound", err)
	}

	for table, col := range map[string]string{
		"project_members":   "project_id",
		"conversations":     "project_id",
		"messages":          "conversation_id",
		"project_providers": "project_id",
	} {
		id := projectID
		if table == "messages" {
			id = convID
		}

		var count int
		if err := pool.QueryRow(
			t.Context(),
			"SELECT count(*) FROM "+table+" WHERE "+col+" = $1", id,
		).Scan(&count); err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}

		if count != 0 {
			t.Errorf("%s: %d rows survived project delete, want 0", table, count)
		}
	}

	if err := store.Delete(t.Context(), uuid.New()); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("Delete(absent): got %v, want ErrNotFound", err)
	}
}

// TestProjectStoreProjectsDetailedByOrg proves the Projects-tab read: correct
// per-project counts, archived status, and the include_archived toggle.
func TestProjectStoreProjectsDetailedByOrg(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewProjectStore(pool)

	orgID := testsupport.SeedOrganization(t, pool)
	projectA := testsupport.SeedProjectInOrg(t, pool, orgID)
	projectB := testsupport.SeedProjectInOrg(t, pool, orgID)

	memberA1 := testsupport.SeedMember(t, pool)
	memberA2 := testsupport.SeedMember(t, pool)

	for _, m := range []uuid.UUID{memberA1, memberA2} {
		if err := store.AddMember(t.Context(), repository.ProjectMembership{
			ProjectID: projectA,
			MemberID:  m,
			Role:      model.RoleUnspecified,
			Domains:   []string{},
		}); err != nil {
			t.Fatalf("AddMember: %v", err)
		}
	}

	if _, err := pool.Exec(
		t.Context(),
		`INSERT INTO conversations (id, project_id, asker_member_id, status, question)
		 VALUES ($1, $2, $3, 'open', 'q?')`,
		uuid.New(), projectA, memberA1,
	); err != nil {
		t.Fatalf("seeding open conversation: %v", err)
	}

	// Seed a RESOLVED conversation so resolved_count (COUNT FILTER status =
	// 'resolved') is exercised alongside the total conversation_count.
	if _, err := pool.Exec(
		t.Context(),
		`INSERT INTO conversations (id, project_id, asker_member_id, status, question)
		 VALUES ($1, $2, $3, 'resolved', 'answered?')`,
		uuid.New(), projectA, memberA1,
	); err != nil {
		t.Fatalf("seeding resolved conversation: %v", err)
	}

	if err := store.SetArchived(t.Context(), projectB, true); err != nil {
		t.Fatalf("SetArchived(projectB): %v", err)
	}

	// Default: archived excluded.
	rows, err := store.ProjectsDetailedByOrg(t.Context(), orgID, false)
	if err != nil {
		t.Fatalf("ProjectsDetailedByOrg: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("ProjectsDetailedByOrg(include_archived=false): got %d rows, want 1", len(rows))
	}

	if rows[0].ID != projectA {
		t.Fatalf("wrong project returned: got %v, want %v", rows[0].ID, projectA)
	}

	if rows[0].Archived {
		t.Error("projectA: Archived = true, want false")
	}

	if rows[0].MemberCount != 2 {
		t.Errorf("MemberCount = %d, want 2", rows[0].MemberCount)
	}

	if rows[0].ConversationCount != 2 {
		t.Errorf("ConversationCount = %d, want 2", rows[0].ConversationCount)
	}

	if rows[0].ResolvedCount != 1 {
		t.Errorf("ResolvedCount = %d, want 1", rows[0].ResolvedCount)
	}

	// include_archived=true surfaces both.
	rows, err = store.ProjectsDetailedByOrg(t.Context(), orgID, true)
	if err != nil {
		t.Fatalf("ProjectsDetailedByOrg(include_archived=true): %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("ProjectsDetailedByOrg(include_archived=true): got %d rows, want 2", len(rows))
	}

	var sawArchivedB bool

	for _, r := range rows {
		if r.ID == projectB {
			sawArchivedB = r.Archived
		}
	}

	if !sawArchivedB {
		t.Error("projectB not returned as archived when include_archived=true")
	}
}

// TestProjectMembersSchemaAfterMigration proves migration 0007 applied: the role
// column is gone and the primary key is exactly (project_id, member_id).
func TestProjectMembersSchemaAfterMigration(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)

	// role column must no longer exist.
	var roleCols int
	if err := pool.QueryRow(
		t.Context(),
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name = 'project_members' AND column_name = 'role'`,
	).Scan(&roleCols); err != nil {
		t.Fatalf("query columns: %v", err)
	}

	if roleCols != 0 {
		t.Errorf("project_members.role still present (%d); migration 0007 did not drop it", roleCols)
	}

	// Primary key columns must be exactly project_id, member_id.
	rows, err := pool.Query(t.Context(),
		`SELECT a.attname
		 FROM pg_index i
		 JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		 WHERE i.indrelid = 'project_members'::regclass AND i.indisprimary
		 ORDER BY a.attname`)
	if err != nil {
		t.Fatalf("query pk: %v", err)
	}
	defer rows.Close()

	var pkCols []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatalf("scan pk col: %v", err)
		}

		pkCols = append(pkCols, col)
	}

	if len(pkCols) != 2 || pkCols[0] != "member_id" || pkCols[1] != "project_id" {
		t.Errorf("primary key = %v, want [member_id project_id]", pkCols)
	}
}
