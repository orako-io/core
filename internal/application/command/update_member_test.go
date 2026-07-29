// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/errs"
)

// updateMemberFakeStore is a minimal memberChannelStore for UpdateMember tests.
type updateMemberFakeStore struct {
	member    model.Member
	byIDErr   error
	updateErr error
	updated   *model.Member
}

func (f *updateMemberFakeStore) ByID(_ context.Context, _ uuid.UUID) (model.Member, error) {
	if f.byIDErr != nil {
		return model.Member{}, f.byIDErr
	}

	return f.member, nil
}

func (f *updateMemberFakeStore) Update(_ context.Context, m model.Member) error {
	if f.updateErr != nil {
		return f.updateErr
	}

	f.updated = &m

	return nil
}

// fakeMemberOrgReader is a minimal memberOrgReader for UpdateMember tests: it
// maps a member id to the org ids of the projects they belong to.
type fakeMemberOrgReader struct {
	memberships map[uuid.UUID][]repository.ProjectWithRole
	err         error
}

func newFakeMemberOrgReader() *fakeMemberOrgReader {
	return &fakeMemberOrgReader{memberships: make(map[uuid.UUID][]repository.ProjectWithRole)}
}

// putInOrg records that memberID belongs to a project owned by orgID.
func (f *fakeMemberOrgReader) putInOrg(memberID, orgID uuid.UUID) {
	f.memberships[memberID] = append(f.memberships[memberID], repository.ProjectWithRole{
		ID: uuid.New(), OrgID: orgID,
	})
}

func (f *fakeMemberOrgReader) ProjectsByMember(_ context.Context, memberID uuid.UUID) ([]repository.ProjectWithRole, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.memberships[memberID], nil
}

func baseMember() model.Member {
	return model.Member{
		ID:              uuid.New(),
		Email:           "sarah@example.com",
		DisplayName:     "Sarah",
		DeliveryChannel: model.DeliveryChannelDashboard,
		Status:          model.MemberStatusActive,
	}
}

func TestUpdateMember_DashboardNeedsNoBinding(t *testing.T) {
	t.Parallel()

	store := &updateMemberFakeStore{member: baseMember()}
	h := MustNewUpdateMemberHandler(store, newFakeMemberOrgReader())

	got, err := h.Handle(context.Background(), UpdateMemberCommand{
		MemberID:        store.member.ID,
		DeliveryChannel: model.DeliveryChannelDashboard,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.DeliveryChannel != string(model.DeliveryChannelDashboard) {
		t.Errorf("channel = %q, want dashboard", got.DeliveryChannel)
	}

	if store.updated == nil {
		t.Fatal("Update was not called")
	}
}

func TestUpdateMember_SlackRequiresBinding(t *testing.T) {
	t.Parallel()

	store := &updateMemberFakeStore{member: baseMember()}
	h := MustNewUpdateMemberHandler(store, newFakeMemberOrgReader())

	_, err := h.Handle(context.Background(), UpdateMemberCommand{
		MemberID:        store.member.ID,
		DeliveryChannel: model.DeliveryChannelSlack,
	})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) || invalid.Field != "slack_user_id" {
		t.Fatalf("want InvalidError on slack_user_id, got %v", err)
	}

	if store.updated != nil {
		t.Error("Update must not be called when validation fails")
	}
}

func TestUpdateMember_SlackWithBindingSucceeds(t *testing.T) {
	t.Parallel()

	store := &updateMemberFakeStore{member: baseMember()}
	h := MustNewUpdateMemberHandler(store, newFakeMemberOrgReader())

	got, err := h.Handle(context.Background(), UpdateMemberCommand{
		MemberID:        store.member.ID,
		DeliveryChannel: model.DeliveryChannelSlack,
		SlackUserID:     "U123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.DeliveryChannel != string(model.DeliveryChannelSlack) || got.SlackUserID != "U123" {
		t.Errorf("got channel=%q slack=%q", got.DeliveryChannel, got.SlackUserID)
	}
}

func TestUpdateMember_TelegramRequiresBinding(t *testing.T) {
	t.Parallel()

	store := &updateMemberFakeStore{member: baseMember()}
	h := MustNewUpdateMemberHandler(store, newFakeMemberOrgReader())

	_, err := h.Handle(context.Background(), UpdateMemberCommand{
		MemberID:        store.member.ID,
		DeliveryChannel: model.DeliveryChannelTelegram,
	})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) || invalid.Field != "telegram_chat_id" {
		t.Fatalf("want InvalidError on telegram_chat_id, got %v", err)
	}
}

// TestUpdateMember_TeamsRequiresBinding mirrors the Slack/Telegram/Discord
// channel-binding invariant for the teams channel — proving the channel is
// now reachable (a bare "not yet available" rejection would fail with
// Field="delivery_channel", not "teams_user_id").
func TestUpdateMember_TeamsRequiresBinding(t *testing.T) {
	t.Parallel()

	store := &updateMemberFakeStore{member: baseMember()}
	h := MustNewUpdateMemberHandler(store, newFakeMemberOrgReader())

	_, err := h.Handle(context.Background(), UpdateMemberCommand{
		MemberID:        store.member.ID,
		DeliveryChannel: model.DeliveryChannelTeams,
	})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) || invalid.Field != "teams_user_id" {
		t.Fatalf("want InvalidError on teams_user_id, got %v", err)
	}
}

// TestUpdateMember_TeamsWithBindingSucceeds proves a member CAN now be set to
// the teams channel once TeamsUserID is provided — the phase-4 Teams pipeline
// was previously unreachable dead code (bare rejection regardless of binding).
func TestUpdateMember_TeamsWithBindingSucceeds(t *testing.T) {
	t.Parallel()

	store := &updateMemberFakeStore{member: baseMember()}
	h := MustNewUpdateMemberHandler(store, newFakeMemberOrgReader())

	got, err := h.Handle(context.Background(), UpdateMemberCommand{
		MemberID:        store.member.ID,
		DeliveryChannel: model.DeliveryChannelTeams,
		TeamsUserID:     "AAD-OBJ-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.DeliveryChannel != string(model.DeliveryChannelTeams) || got.TeamsUserID != "AAD-OBJ-1" {
		t.Errorf("got channel=%q teams=%q", got.DeliveryChannel, got.TeamsUserID)
	}
}

func TestUpdateMember_UnknownChannelRejected(t *testing.T) {
	t.Parallel()

	store := &updateMemberFakeStore{member: baseMember()}
	h := MustNewUpdateMemberHandler(store, newFakeMemberOrgReader())

	_, err := h.Handle(context.Background(), UpdateMemberCommand{
		MemberID:        store.member.ID,
		DeliveryChannel: model.DeliveryChannel("carrier-pigeon"),
	})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) || invalid.Field != "delivery_channel" {
		t.Fatalf("want InvalidError on delivery_channel, got %v", err)
	}
}

// TestUpdateMember_TeamsDiscordBindingsClearSentinel proves the two new
// bindings accept the "empty = unchanged, '-' = clear" convention.
func TestUpdateMember_TeamsDiscordBindingsClearSentinel(t *testing.T) {
	t.Parallel()

	existing := baseMember()
	existing.TeamsUserID = "AAD-OBJ-1"
	existing.DiscordUserID = "111222333"
	store := &updateMemberFakeStore{member: existing}
	h := MustNewUpdateMemberHandler(store, newFakeMemberOrgReader())

	// Empty leaves both unchanged.
	got, err := h.Handle(context.Background(), UpdateMemberCommand{MemberID: existing.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.TeamsUserID != "AAD-OBJ-1" || got.DiscordUserID != "111222333" {
		t.Errorf("bindings changed unexpectedly: teams=%q discord=%q", got.TeamsUserID, got.DiscordUserID)
	}

	// A new value replaces the Teams binding; Discord stays untouched.
	got, err = h.Handle(context.Background(), UpdateMemberCommand{MemberID: existing.ID, TeamsUserID: "AAD-OBJ-2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.TeamsUserID != "AAD-OBJ-2" {
		t.Errorf("TeamsUserID = %q, want AAD-OBJ-2", got.TeamsUserID)
	}

	if got.DiscordUserID != "111222333" {
		t.Errorf("DiscordUserID changed unexpectedly to %q", got.DiscordUserID)
	}

	// "-" clears the Discord binding.
	got, err = h.Handle(context.Background(), UpdateMemberCommand{MemberID: existing.ID, DiscordUserID: "-"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.DiscordUserID != "" {
		t.Errorf("DiscordUserID after clear = %q, want empty", got.DiscordUserID)
	}
}

// TestUpdateMember_DiscordRequiresBinding mirrors the Slack/Telegram
// channel-binding invariant for the new discord channel.
func TestUpdateMember_DiscordRequiresBinding(t *testing.T) {
	t.Parallel()

	store := &updateMemberFakeStore{member: baseMember()}
	h := MustNewUpdateMemberHandler(store, newFakeMemberOrgReader())

	_, err := h.Handle(context.Background(), UpdateMemberCommand{
		MemberID:        store.member.ID,
		DeliveryChannel: model.DeliveryChannelDiscord,
	})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) || invalid.Field != "discord_user_id" {
		t.Fatalf("want InvalidError on discord_user_id, got %v", err)
	}
}

// TestUpdateMember_BindingChangeClearsBindingError proves changing any
// binding clears a stale binding_error.
func TestUpdateMember_BindingChangeClearsBindingError(t *testing.T) {
	t.Parallel()

	existing := baseMember()
	existing.BindingError = "slack API error: channel_not_found"
	store := &updateMemberFakeStore{member: existing}
	h := MustNewUpdateMemberHandler(store, newFakeMemberOrgReader())

	got, err := h.Handle(context.Background(), UpdateMemberCommand{
		MemberID:    existing.ID,
		SlackUserID: "U123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.BindingError != "" {
		t.Errorf("BindingError = %q, want cleared", got.BindingError)
	}
}

// TestUpdateMember_NonAdminCannotTargetAnotherMember proves TargetMemberID
// requires IsOrgAdmin when it names someone other than the caller.
func TestUpdateMember_NonAdminCannotTargetAnotherMember(t *testing.T) {
	t.Parallel()

	caller := uuid.New()
	target := baseMember()
	store := &updateMemberFakeStore{member: target}
	h := MustNewUpdateMemberHandler(store, newFakeMemberOrgReader())

	_, err := h.Handle(context.Background(), UpdateMemberCommand{
		MemberID:       caller,
		TargetMemberID: target.ID,
		IsOrgAdmin:     false,
		DiscordUserID:  "999888777",
	})

	var forbidden errs.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("want ForbiddenError, got %v", err)
	}

	if store.updated != nil {
		t.Error("a forbidden update must not write")
	}
}

// TestUpdateMember_OrgAdminCanFillAnotherMembersBinding proves an org admin
// may set another member's Discord binding through TargetMemberID, PROVIDED
// the target belongs to the admin's own organization.
func TestUpdateMember_OrgAdminCanFillAnotherMembersBinding(t *testing.T) {
	t.Parallel()

	caller := uuid.New()
	orgID := uuid.New()
	target := baseMember()

	store := &updateMemberFakeStore{member: target}
	orgs := newFakeMemberOrgReader()
	orgs.putInOrg(target.ID, orgID)
	h := MustNewUpdateMemberHandler(store, orgs)

	got, err := h.Handle(context.Background(), UpdateMemberCommand{
		MemberID:       caller,
		TargetMemberID: target.ID,
		IsOrgAdmin:     true,
		OrgID:          orgID,
		DiscordUserID:  "999888777",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.ID != target.ID {
		t.Errorf("updated member id = %v, want the target %v", got.ID, target.ID)
	}

	if got.DiscordUserID != "999888777" {
		t.Errorf("DiscordUserID = %q, want 999888777", got.DiscordUserID)
	}
}

// TestUpdateMember_CrossTenantAdminForbidden is the IDOR regression test: an
// org-A admin targeting a member who belongs ONLY to org B must be rejected
// BEFORE the member is loaded or written — IsOrgAdmin alone must never be
// enough to reach an arbitrary UUID across tenants.
func TestUpdateMember_CrossTenantAdminForbidden(t *testing.T) {
	t.Parallel()

	caller := uuid.New()
	orgA := uuid.New()
	orgB := uuid.New()
	target := baseMember()

	store := &updateMemberFakeStore{member: target}
	orgs := newFakeMemberOrgReader()
	orgs.putInOrg(target.ID, orgB) // target belongs to org B only
	h := MustNewUpdateMemberHandler(store, orgs)

	_, err := h.Handle(context.Background(), UpdateMemberCommand{
		MemberID:       caller,
		TargetMemberID: target.ID,
		IsOrgAdmin:     true,
		OrgID:          orgA, // caller is an admin of org A
		DiscordUserID:  "999888777",
	})

	var forbidden errs.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("want ForbiddenError for cross-tenant admin, got %v", err)
	}

	if store.updated != nil {
		t.Error("a cross-tenant update must never write")
	}
}

// TestUpdateMember_CrossTenantAdminForbidden_NoMembershipAtAll proves a
// target with NO project membership anywhere (e.g. a stray/foreign UUID) is
// also rejected — fail closed, not fail open, when membership resolution
// comes back empty.
func TestUpdateMember_CrossTenantAdminForbidden_NoMembershipAtAll(t *testing.T) {
	t.Parallel()

	caller := uuid.New()
	orgA := uuid.New()
	target := baseMember() // never registered in orgs

	store := &updateMemberFakeStore{member: target}
	h := MustNewUpdateMemberHandler(store, newFakeMemberOrgReader())

	_, err := h.Handle(context.Background(), UpdateMemberCommand{
		MemberID:       caller,
		TargetMemberID: target.ID,
		IsOrgAdmin:     true,
		OrgID:          orgA,
		DiscordUserID:  "999888777",
	})

	var forbidden errs.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("want ForbiddenError when target has no membership, got %v", err)
	}

	if store.updated != nil {
		t.Error("a target with no resolvable org must never be written")
	}
}

// TestUpdateMember_SelfUpdatePathUnaffectedByTargetMemberID proves the
// self-service path (TargetMemberID unset) is byte-identical to before this
// phase: it works with IsOrgAdmin=false, matching the on-the-wire default for
// a non-admin caller.
func TestUpdateMember_SelfUpdatePathUnaffectedByTargetMemberID(t *testing.T) {
	t.Parallel()

	store := &updateMemberFakeStore{member: baseMember()}
	h := MustNewUpdateMemberHandler(store, newFakeMemberOrgReader())

	got, err := h.Handle(context.Background(), UpdateMemberCommand{
		MemberID:    store.member.ID,
		IsOrgAdmin:  false,
		DisplayName: "Sarah Connor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.DisplayName != "Sarah Connor" {
		t.Errorf("DisplayName = %q, want Sarah Connor", got.DisplayName)
	}
}

func TestUpdateMember_EmptyChannelKeepsExisting(t *testing.T) {
	t.Parallel()

	existing := baseMember()
	existing.DeliveryChannel = model.DeliveryChannelSlack
	existing.SlackUserID = "U999"
	store := &updateMemberFakeStore{member: existing}
	h := MustNewUpdateMemberHandler(store, newFakeMemberOrgReader())

	got, err := h.Handle(context.Background(), UpdateMemberCommand{
		MemberID:    existing.ID,
		DisplayName: "Sarah Connor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.DeliveryChannel != string(model.DeliveryChannelSlack) {
		t.Errorf("channel changed unexpectedly to %q", got.DeliveryChannel)
	}

	if got.DisplayName != "Sarah Connor" {
		t.Errorf("display name = %q, want updated", got.DisplayName)
	}
}
