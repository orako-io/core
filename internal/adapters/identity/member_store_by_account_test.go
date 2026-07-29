// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/identity"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestMemberStoreByAccountID proves the account_id lookup that the JWT resolver
// relies on: after an account creates an org (its creator member is linked by
// account_id with an empty email), ByAccountID returns that active member; when the
// same account creates a second org (a second member row), ByAccountID stays
// deterministic and — among members of equal liveness — returns the most-recent
// one; an account with no member is ErrNotFound.
func TestMemberStoreByAccountID(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	memberStore := identity.NewMemberStore(pool)
	orgStore := newOrgSeeder(pool)

	accountID := testsupport.SeedAccountForOrg(t, pool)

	// ── First org: creates the account-linked creator member. ────────────────
	firstOrg, err := model.NewOrganization(uuid.New(), "First Org")
	if err != nil {
		t.Fatalf("NewOrganization: %v", err)
	}

	firstProject, err := model.NewProjectInOrg(uuid.New(), "global", firstOrg.ID)
	if err != nil {
		t.Fatalf("NewProjectInOrg: %v", err)
	}

	firstMemberID := uuid.New()

	firstMember, err := model.NewActiveMember(firstMemberID, accountID)
	if err != nil {
		t.Fatalf("NewActiveMember: %v", err)
	}

	if err := orgStore.CreateOrganizationWithGlobalProject(
		t.Context(), firstOrg, accountID, firstProject, firstMember,
	); err != nil {
		t.Fatalf("CreateOrganizationWithGlobalProject (first): %v", err)
	}

	got, err := memberStore.ByAccountID(t.Context(), accountID)
	if err != nil {
		t.Fatalf("ByAccountID (single member): %v", err)
	}

	if got.ID != firstMemberID {
		t.Errorf("ByAccountID: got member %s, want the creator member %s", got.ID, firstMemberID)
	}

	if got.Email != "" {
		t.Errorf("creator member email: got %q, want empty (linked by account_id, not email)", got.Email)
	}

	if got.Status != model.MemberStatusActive {
		t.Errorf("creator member status: got %q, want %q", got.Status, model.MemberStatusActive)
	}

	// ── Second org for the SAME account: a second member row with a later
	// created_at. account_id is non-unique; both members are active (equal
	// liveness), so ByAccountID must resolve to the most-recent row deterministically
	// (ORDER BY <dead> ASC, created_at DESC LIMIT 1). ─────────────────────────
	//
	// created_at defaults to NOW(); ensure a strictly later timestamp on the
	// second member so the ordering is unambiguous within the test.
	time.Sleep(10 * time.Millisecond)

	secondOrg, err := model.NewOrganization(uuid.New(), "Second Org")
	if err != nil {
		t.Fatalf("NewOrganization (second): %v", err)
	}

	secondProject, err := model.NewProjectInOrg(uuid.New(), "global", secondOrg.ID)
	if err != nil {
		t.Fatalf("NewProjectInOrg (second): %v", err)
	}

	secondMemberID := uuid.New()

	secondMember, err := model.NewActiveMember(secondMemberID, accountID)
	if err != nil {
		t.Fatalf("NewActiveMember (second): %v", err)
	}

	if err := orgStore.CreateOrganizationWithGlobalProject(
		t.Context(), secondOrg, accountID, secondProject, secondMember,
	); err != nil {
		t.Fatalf("CreateOrganizationWithGlobalProject (second): %v", err)
	}

	// Determinism: repeat the lookup several times; among equally-live members it
	// must always be the most-recent one.
	for i := range 5 {
		again, err := memberStore.ByAccountID(t.Context(), accountID)
		if err != nil {
			t.Fatalf("ByAccountID (two members, iter %d): %v", i, err)
		}

		if again.ID != secondMemberID {
			t.Errorf("ByAccountID iter %d: got %s, want the most-recent member %s deterministically",
				i, again.ID, secondMemberID)
		}
	}

	// ── An account that owns no member is ErrNotFound. ────────────────────────
	if _, err := memberStore.ByAccountID(t.Context(), uuid.New()); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("ByAccountID (no member): got %v, want ErrNotFound", err)
	}
}

// TestMemberStoreByAccountIDPrefersLiveMember proves the offboarding-aware
// ordering: when an account owns BOTH a dead (offboarded) member and a live one —
// the shape produced when a re-joined human accumulates a fresh membership beside
// the revoked ghost — ByAccountID returns the LIVE member, regardless of which
// was created first. Without this, the JWT resolver would keep resolving the dead
// ghost and hard-fail with "member access has been revoked".
func TestMemberStoreByAccountIDPrefersLiveMember(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	memberStore := identity.NewMemberStore(pool)
	orgStore := newOrgSeeder(pool)

	accountID := testsupport.SeedAccountForOrg(t, pool)

	// ── The account's FIRST member: created earliest, then offboarded. ────────
	org, err := model.NewOrganization(uuid.New(), "Ghost Org")
	if err != nil {
		t.Fatalf("NewOrganization: %v", err)
	}

	project, err := model.NewProjectInOrg(uuid.New(), "global", org.ID)
	if err != nil {
		t.Fatalf("NewProjectInOrg: %v", err)
	}

	deadMemberID := uuid.New()

	deadMember, err := model.NewActiveMember(deadMemberID, accountID)
	if err != nil {
		t.Fatalf("NewActiveMember (dead): %v", err)
	}

	if err := orgStore.CreateOrganizationWithGlobalProject(
		t.Context(), org, accountID, project, deadMember,
	); err != nil {
		t.Fatalf("CreateOrganizationWithGlobalProject: %v", err)
	}

	// Offboard it: flip the earliest member to removed (a non-CanAuthenticate ghost).
	deadMember.Status = model.MemberStatusRemoved
	if err := memberStore.Update(t.Context(), deadMember); err != nil {
		t.Fatalf("Update (offboard): %v", err)
	}

	// ── A fresh LIVE member for the SAME account, created later. ──────────────
	time.Sleep(10 * time.Millisecond)

	liveMemberID := uuid.New()

	liveMember, err := model.NewPendingMember(liveMemberID, accountID, "", "Re-Joiner")
	if err != nil {
		t.Fatalf("NewPendingMember: %v", err)
	}

	if err := memberStore.CreateWithAccount(t.Context(), liveMember); err != nil {
		t.Fatalf("CreateWithAccount (live): %v", err)
	}

	// ByAccountID must return the live member, not the earlier dead ghost.
	got, err := memberStore.ByAccountID(t.Context(), accountID)
	if err != nil {
		t.Fatalf("ByAccountID (dead + live): %v", err)
	}

	if got.ID != liveMemberID {
		t.Errorf("ByAccountID: got %s (status %q), want the live member %s over the dead ghost %s",
			got.ID, got.Status, liveMemberID, deadMemberID)
	}

	if !got.Status.CanAuthenticate() {
		t.Errorf("resolved member status = %q, want an authenticatable (live) member", got.Status)
	}
}
