// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"testing"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
)

// fakeProjectProvisioner records the project and creator-membership writes the
// handler composes under its Transactor.
type fakeProjectProvisioner struct {
	project         model.Project
	creatorMemberID uuid.UUID
	called          bool
}

func (f *fakeProjectProvisioner) CreateInOrg(_ context.Context, project model.Project) error {
	f.called = true
	f.project = project

	return nil
}

func (f *fakeProjectProvisioner) AddMember(_ context.Context, m repository.ProjectMembership) error {
	f.creatorMemberID = m.MemberID

	return nil
}

func TestCreateProject_HappyPath(t *testing.T) {
	t.Parallel()

	creator := &fakeProjectProvisioner{}
	h := MustNewCreateProjectHandler(creator, fakeTransactor{}, nil)

	orgID := uuid.New()
	memberID := uuid.New()

	result, err := h.Handle(t.Context(), CreateProjectCommand{Name: "orako-core", OrgID: orgID, CreatorMemberID: memberID})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if result.ProjectID == uuid.Nil {
		t.Fatal("ProjectID is nil")
	}

	if !creator.called || creator.project.OrgID != orgID || creator.creatorMemberID != memberID {
		t.Errorf("creator called with wrong args: called=%v orgID=%v memberID=%v", creator.called, creator.project.OrgID, creator.creatorMemberID)
	}
}

func TestCreateProject_BlankName(t *testing.T) {
	t.Parallel()

	h := MustNewCreateProjectHandler(&fakeProjectProvisioner{}, fakeTransactor{}, nil)

	_, err := h.Handle(t.Context(), CreateProjectCommand{Name: "  ", OrgID: uuid.New(), CreatorMemberID: uuid.New()})
	if err == nil {
		t.Fatal("expected error for blank name")
	}
}

func TestCreateProject_RequiresOrg(t *testing.T) {
	t.Parallel()

	h := MustNewCreateProjectHandler(&fakeProjectProvisioner{}, fakeTransactor{}, nil)

	_, err := h.Handle(t.Context(), CreateProjectCommand{Name: "x", CreatorMemberID: uuid.New()})
	if err == nil {
		t.Fatal("expected error for nil org id")
	}
}

func TestAddMember_CreatesNewMemberAndEmitsInvited(t *testing.T) {
	t.Parallel()

	writer := newFakeMemberWriter()
	bus := &fakeEventBus{}
	h := MustNewAddMemberHandler(writer, fakeTransactor{}, &updateMemberFakeStore{member: baseMember()}, bus, nil, nil)

	projectID := uuid.New()

	result, err := h.Handle(t.Context(), AddMemberCommand{
		ProjectID:   projectID,
		Email:       "alice@example.com",
		DisplayName: "Alice",
		Domains:     []string{"backend"},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if result.MemberID == uuid.Nil {
		t.Fatal("MemberID is nil")
	}

	// Member created.
	if _, ok := writer.members[result.MemberID]; !ok {
		t.Error("member not stored in writer")
	}

	// Membership recorded with the supplied expertise tags.
	if len(writer.memberships) == 0 {
		t.Fatal("membership not stored")
	}

	if got := writer.memberships[0].Domains; len(got) != 1 || got[0] != "backend" {
		t.Errorf("Domains = %v, want [backend]", got)
	}

	// MemberLifecycle(INVITED) emitted.
	env, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_MEMBER_LIFECYCLE)
	if !ok {
		t.Fatal("MemberLifecycle event not published")
	}

	ml := env.GetMemberLifecycle()
	if ml == nil {
		t.Fatal("MemberLifecycle payload is nil")
	}

	if ml.Transition != orakov1.MemberTransition_MEMBER_TRANSITION_INVITED {
		t.Errorf("Transition = %v, want INVITED", ml.Transition)
	}

	// Role is retired (Part 2): the lifecycle event no longer carries one.
	if ml.GetRole() != orakov1.Role_ROLE_UNSPECIFIED {
		t.Errorf("Role = %v, want UNSPECIFIED (role retired)", ml.GetRole())
	}
}

func TestAddMember_ReusesExistingMember(t *testing.T) {
	t.Parallel()

	writer := newFakeMemberWriter()
	existingID := uuid.New()
	existing, _ := model.NewMember(existingID, "bob@example.com", "Bob")
	writer.members[existingID] = existing
	writer.byEmail["bob@example.com"] = existing

	h := MustNewAddMemberHandler(writer, fakeTransactor{}, &updateMemberFakeStore{member: baseMember()}, &fakeEventBus{}, nil, nil)

	result, err := h.Handle(t.Context(), AddMemberCommand{
		ProjectID:   uuid.New(),
		Email:       "bob@example.com",
		DisplayName: "Bob",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if result.MemberID != existingID {
		t.Errorf("MemberID = %v, want existing %v", result.MemberID, existingID)
	}

	// No new member should have been created.
	if len(writer.members) != 1 {
		t.Errorf("member count = %d, want 1 (no new member created)", len(writer.members))
	}
}

// TestAddMember_LifecycleEmissionMatrix guards the one-invitation-email-per-
// member rule and the added-to-project notification:
//   - a PENDING member added to another project is SILENT (the invite modal
//     loops over projects; this used to send one invitation email per project),
//   - an explicit Resend of a pending invitation re-emits INVITED,
//   - an ACTIVE member added to a new project emits ADDED_TO_PROJECT (the
//     "you've been added" notification, coalesced downstream),
//   - re-adding the SAME membership is silent.
func TestAddMember_LifecycleEmissionMatrix(t *testing.T) {
	t.Parallel()

	setup := func(status model.MemberStatus, sameMembership bool) (*fakeEventBus, AddMemberHandler) {
		existingID := uuid.New()
		existing, _ := model.NewMember(existingID, "bob@example.com", "Bob")
		existing.Status = status

		writer := newFakeMemberWriter()
		writer.members[existingID] = existing
		writer.byEmail["bob@example.com"] = existing
		writer.duplicateAdd = sameMembership
		bus := &fakeEventBus{}

		return bus, MustNewAddMemberHandler(writer, fakeTransactor{}, &updateMemberFakeStore{member: existing}, bus, nil, nil)
	}

	handle := func(t *testing.T, h AddMemberHandler, resend bool) {
		t.Helper()

		if _, err := h.Handle(t.Context(), AddMemberCommand{
			ProjectID:   uuid.New(),
			Email:       "bob@example.com",
			DisplayName: "Bob",
			Resend:      resend,
		}); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}

	t.Run("pending_member_new_project_is_silent", func(t *testing.T) {
		t.Parallel()

		bus, h := setup(model.MemberStatusInvited, false)
		handle(t, h, false)

		if _, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_MEMBER_LIFECYCLE); ok {
			t.Error("pending member on a new project must NOT emit (one invitation email per member, not per project)")
		}
	})

	t.Run("explicit_resend_re_emits_invited", func(t *testing.T) {
		t.Parallel()

		bus, h := setup(model.MemberStatusInvited, false)
		handle(t, h, true)

		env, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_MEMBER_LIFECYCLE)
		if !ok {
			t.Fatal("explicit resend must publish INVITED so the email goes out")
		}

		if env.GetMemberLifecycle().GetTransition() != orakov1.MemberTransition_MEMBER_TRANSITION_INVITED {
			t.Errorf("Transition = %v, want INVITED", env.GetMemberLifecycle().GetTransition())
		}
	})

	t.Run("active_member_new_project_emits_added", func(t *testing.T) {
		t.Parallel()

		bus, h := setup(model.MemberStatusActive, false)
		handle(t, h, false)

		env, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_MEMBER_LIFECYCLE)
		if !ok {
			t.Fatal("active member on a new project must emit ADDED_TO_PROJECT")
		}

		if env.GetMemberLifecycle().GetTransition() != orakov1.MemberTransition_MEMBER_TRANSITION_ADDED_TO_PROJECT {
			t.Errorf("Transition = %v, want ADDED_TO_PROJECT", env.GetMemberLifecycle().GetTransition())
		}
	})

	t.Run("same_membership_re_add_is_silent", func(t *testing.T) {
		t.Parallel()

		bus, h := setup(model.MemberStatusActive, true)
		handle(t, h, false)

		if _, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_MEMBER_LIFECYCLE); ok {
			t.Error("re-adding the same membership must stay silent")
		}
	})
}

func TestRemoveMember_ArchiveDefaultPath(t *testing.T) {
	t.Parallel()

	memberRepo := newFakeMemberRepo()
	bus := &fakeEventBus{}

	memberID := uuid.New()
	m, _ := model.NewMember(memberID, "charlie@example.com", "Charlie")
	memberRepo.members[memberID] = m
	memberRepo.byEmail["charlie@example.com"] = m

	h := MustNewRemoveMemberHandler(memberRepo, bus)
	projectID := uuid.New()

	if err := h.Handle(t.Context(), RemoveMemberCommand{
		MemberID:  memberID,
		OrgID:     uuid.New(),
		ProjectID: projectID,
		Purge:     false,
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	stored := memberRepo.members[memberID]
	if stored.Status != model.MemberStatusRemoved {
		t.Errorf("Status = %q, want removed", stored.Status)
	}

	// Email still present (archive, not purge).
	if stored.Email == "" {
		t.Error("Email cleared on archive; should only be cleared on purge")
	}

	env, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_MEMBER_LIFECYCLE)
	if !ok {
		t.Fatal("MemberLifecycle event not published")
	}

	if env.GetMemberLifecycle().GetTransition() != orakov1.MemberTransition_MEMBER_TRANSITION_REMOVED {
		t.Error("expected REMOVED transition")
	}
}

func TestRemoveMember_PurgeClearsPII(t *testing.T) {
	t.Parallel()

	memberRepo := newFakeMemberRepo()
	bus := &fakeEventBus{}

	memberID := uuid.New()
	m, _ := model.NewMember(memberID, "dave@example.com", "Dave")
	m.SlackUserID = "U123"
	memberRepo.members[memberID] = m
	memberRepo.byEmail["dave@example.com"] = m

	h := MustNewRemoveMemberHandler(memberRepo, bus)

	if err := h.Handle(t.Context(), RemoveMemberCommand{
		MemberID:  memberID,
		OrgID:     uuid.New(),
		ProjectID: uuid.New(),
		Purge:     true,
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	stored := memberRepo.members[memberID]
	if stored.Status != model.MemberStatusPurged {
		t.Errorf("Status = %q, want purged", stored.Status)
	}

	if stored.Email != "" {
		t.Errorf("Email = %q, want empty after purge", stored.Email)
	}

	if stored.DisplayName != "" {
		t.Errorf("DisplayName = %q, want empty after purge", stored.DisplayName)
	}

	if stored.SlackUserID != "" {
		t.Errorf("SlackUserID = %q, want empty after purge", stored.SlackUserID)
	}

	env, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_MEMBER_LIFECYCLE)
	if !ok {
		t.Fatal("MemberLifecycle event not published")
	}

	if env.GetMemberLifecycle().GetTransition() != orakov1.MemberTransition_MEMBER_TRANSITION_PURGED {
		t.Error("expected PURGED transition")
	}
}

func TestAssignRole_SetsTagsAndEmitsRoleChanged(t *testing.T) {
	t.Parallel()

	projectRepo := newFakeProjectRepo()
	bus := &fakeEventBus{}
	h := MustNewAssignRoleHandler(projectRepo, bus)

	projectID := uuid.New()
	memberID := uuid.New()

	// Pre-enrol the member: set-tags is an UPDATE, not an INSERT.
	projectRepo.memberships = append(projectRepo.memberships, repository.ProjectMembership{
		ProjectID: projectID,
		MemberID:  memberID,
		Role:      model.RoleUnspecified,
		Domains:   []string{"frontend"},
	})

	if err := h.Handle(t.Context(), AssignRoleCommand{
		ProjectID: projectID,
		MemberID:  memberID,
		Domains:   []string{"backend", "devops"},
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// Tags replaced (not appended).
	got := projectRepo.memberships[0].Domains
	if len(got) != 2 || got[0] != "backend" || got[1] != "devops" {
		t.Errorf("Domains = %v, want [backend devops]", got)
	}

	env, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_MEMBER_LIFECYCLE)
	if !ok {
		t.Fatal("MemberLifecycle event not published")
	}

	ml := env.GetMemberLifecycle()
	if ml.GetTransition() != orakov1.MemberTransition_MEMBER_TRANSITION_ROLE_CHANGED {
		t.Errorf("Transition = %v, want ROLE_CHANGED", ml.GetTransition())
	}

	// Role is retired (Part 2): the event no longer carries one.
	if ml.GetRole() != orakov1.Role_ROLE_UNSPECIFIED {
		t.Errorf("Role = %v, want UNSPECIFIED (role retired)", ml.GetRole())
	}
}

func TestAssignRole_NotEnrolledReturnsError(t *testing.T) {
	t.Parallel()

	projectRepo := newFakeProjectRepo()
	h := MustNewAssignRoleHandler(projectRepo, &fakeEventBus{})

	err := h.Handle(t.Context(), AssignRoleCommand{
		ProjectID: uuid.New(),
		MemberID:  uuid.New(),
		Domains:   []string{"backend"},
	})
	if err == nil {
		t.Fatal("expected error setting tags for a non-enrolled member")
	}
}
