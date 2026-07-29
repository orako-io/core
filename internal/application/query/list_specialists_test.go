// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
)

// TestListExperts_ReturnsAllMembersWithDomains proves the Part 2 rework:
// every project member is routable (no permission-role filter) and their
// expertise tags (domains) are surfaced. This is the fix for the Part 1 side
// effect where the RoleSpecialist/RoleLead filter returned an empty list.
func TestListExperts_ReturnsAllMembersWithDomains(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	m1 := uuid.New()
	m2 := uuid.New()

	projectReader := &fakeProjectReader{
		memberships: []repository.ProjectMembership{
			// Role is Unspecified for every membership now (project role retired).
			{ProjectID: projectID, MemberID: m1, Role: model.RoleUnspecified, Domains: []string{"Backend", "CTO"}},
			{ProjectID: projectID, MemberID: m2, Role: model.RoleUnspecified, Domains: []string{}},
		},
	}

	memberReader := newFakeMemberReader()
	alice, _ := model.NewMember(m1, "alice@example.com", "Alice")
	alice.Status = model.MemberStatusActive
	bob, _ := model.NewMember(m2, "bob@example.com", "Bob")
	bob.Status = model.MemberStatusActive
	memberReader.members[m1] = alice
	memberReader.members[m2] = bob

	h := MustNewListExpertsHandler(projectReader, memberReader, newFakePresenceReader())

	specs, err := h.Handle(t.Context(), ListExpertsQuery{ProjectID: projectID})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// Both members returned — no role filter.
	if len(specs) != 2 {
		t.Fatalf("experts len = %d, want 2 (every member is routable)", len(specs))
	}

	byID := map[uuid.UUID]Expert{}
	for _, s := range specs {
		byID[s.MemberID] = s
	}

	if got := byID[m1].Domains; len(got) != 2 || got[0] != "Backend" || got[1] != "CTO" {
		t.Errorf("m1 Domains = %v, want [Backend CTO]", got)
	}

	if got := byID[m2].Domains; len(got) != 0 {
		t.Errorf("m2 Domains = %v, want empty", got)
	}
}

// TestListExperts_OnlyRoutableActive proves the directory returns ONLY routable
// (active) members: removed, purged, pending, on_leave and deactivated members
// are all filtered out, so the agent is never offered a target it cannot reach.
func TestListExperts_OnlyRoutableActive(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	active := uuid.New()
	removed := uuid.New()
	purged := uuid.New()
	pending := uuid.New()
	onLeave := uuid.New()
	deactivated := uuid.New()

	projectReader := &fakeProjectReader{
		memberships: []repository.ProjectMembership{
			{ProjectID: projectID, MemberID: active, Role: model.RoleUnspecified, Domains: []string{"QA"}},
			{ProjectID: projectID, MemberID: removed, Role: model.RoleUnspecified, Domains: []string{"Frontend"}},
			{ProjectID: projectID, MemberID: purged, Role: model.RoleUnspecified, Domains: []string{"DevOps"}},
			{ProjectID: projectID, MemberID: pending, Role: model.RoleUnspecified, Domains: []string{"Backend"}},
			{ProjectID: projectID, MemberID: onLeave, Role: model.RoleUnspecified, Domains: []string{"SRE"}},
			{ProjectID: projectID, MemberID: deactivated, Role: model.RoleUnspecified, Domains: []string{"Data"}},
		},
	}

	memberReader := newFakeMemberReader()

	mk := func(id uuid.UUID, name string, st model.MemberStatus) {
		m, _ := model.NewMember(id, name+"@example.com", name)
		m.Status = st
		memberReader.members[id] = m
	}
	mk(active, "Active", model.MemberStatusActive)
	mk(removed, "Removed", model.MemberStatusRemoved)
	mk(purged, "Purged", model.MemberStatusPurged)
	mk(pending, "Pending", model.MemberStatusPending)
	mk(onLeave, "OnLeave", model.MemberStatusOnLeave)
	mk(deactivated, "Deactivated", model.MemberStatusDeactivated)

	h := MustNewListExpertsHandler(projectReader, memberReader, newFakePresenceReader())

	specs, err := h.Handle(t.Context(), ListExpertsQuery{ProjectID: projectID})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(specs) != 1 || specs[0].MemberID != active {
		t.Fatalf("experts = %+v, want only the active member (pending/on_leave/deactivated/removed/purged excluded)", specs)
	}
}
