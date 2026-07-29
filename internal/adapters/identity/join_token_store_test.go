// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/identity"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestJoinTokenStoreLifecycle proves the full store contract against real
// Postgres: create → resolve → rotate (one live per org) → revoke → active
// lookup, plus the partial unique index that guarantees at most one live code
// per org.
func TestJoinTokenStoreLifecycle(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewJoinTokenStore(pool)
	seeder := newOrgSeeder(pool)

	accountID := testsupport.SeedAccountForOrg(t, pool)

	org, err := model.NewOrganization(uuid.New(), "Join Org")
	if err != nil {
		t.Fatalf("NewOrganization: %v", err)
	}

	project, err := model.NewProjectInOrg(uuid.New(), "global", org.ID)
	if err != nil {
		t.Fatalf("NewProjectInOrg: %v", err)
	}

	creator, err := model.NewActiveMember(uuid.New(), accountID)
	if err != nil {
		t.Fatalf("NewActiveMember: %v", err)
	}

	if err := seeder.CreateOrganizationWithGlobalProject(t.Context(), org, accountID, project, creator); err != nil {
		t.Fatalf("seeding org: %v", err)
	}

	// ── No live token yet. ────────────────────────────────────────────────────
	if _, ok, err := store.ActiveJoinToken(t.Context(), org.ID); err != nil || ok {
		t.Fatalf("ActiveJoinToken (none): ok=%v err=%v, want ok=false err=nil", ok, err)
	}

	// ── Create → resolvable to org/project, not revoked. ──────────────────────
	tok1, err := store.CreateJoinToken(t.Context(), org.ID, project.ID, creator.ID)
	if err != nil {
		t.Fatalf("CreateJoinToken: %v", err)
	}

	resolved, err := store.JoinTokenByToken(t.Context(), tok1)
	if err != nil {
		t.Fatalf("JoinTokenByToken: %v", err)
	}

	if resolved.OrgID != org.ID || resolved.ProjectID != project.ID {
		t.Errorf("resolved org/project = %s/%s, want %s/%s", resolved.OrgID, resolved.ProjectID, org.ID, project.ID)
	}

	if resolved.Revoked {
		t.Error("freshly minted token reported revoked")
	}

	// ── Rotate: a second create revokes the first (one live per org). ─────────
	tok2, err := store.CreateJoinToken(t.Context(), org.ID, project.ID, creator.ID)
	if err != nil {
		t.Fatalf("CreateJoinToken (rotate): %v", err)
	}

	if tok2 == tok1 {
		t.Fatal("rotate produced the same token string")
	}

	old, err := store.JoinTokenByToken(t.Context(), tok1)
	if err != nil {
		t.Fatalf("JoinTokenByToken (old): %v", err)
	}

	if !old.Revoked {
		t.Error("the prior token must be revoked after a rotate (one live code per org)")
	}

	active, ok, err := store.ActiveJoinToken(t.Context(), org.ID)
	if err != nil || !ok {
		t.Fatalf("ActiveJoinToken (after rotate): ok=%v err=%v", ok, err)
	}

	if active.Token != tok2 {
		t.Errorf("active token = %s, want the rotated-in %s", active.Token, tok2)
	}

	// ── Revoke: no live token remains; the code no longer resolves as live. ───
	if err := store.RevokeJoinToken(t.Context(), org.ID); err != nil {
		t.Fatalf("RevokeJoinToken: %v", err)
	}

	if _, ok, err := store.ActiveJoinToken(t.Context(), org.ID); err != nil || ok {
		t.Fatalf("ActiveJoinToken (after revoke): ok=%v err=%v, want ok=false", ok, err)
	}

	revoked, err := store.JoinTokenByToken(t.Context(), tok2)
	if err != nil {
		t.Fatalf("JoinTokenByToken (revoked): %v", err)
	}

	if !revoked.Revoked {
		t.Error("token must report revoked after RevokeJoinToken")
	}

	// Revoke is idempotent.
	if err := store.RevokeJoinToken(t.Context(), org.ID); err != nil {
		t.Fatalf("RevokeJoinToken (idempotent): %v", err)
	}

	// ── Unknown token → ErrNotFound. ──────────────────────────────────────────
	if _, err := store.JoinTokenByToken(t.Context(), "does-not-exist"); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("JoinTokenByToken (unknown): got %v, want ErrNotFound", err)
	}
}
