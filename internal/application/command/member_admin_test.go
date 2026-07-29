// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// --- fakes ---

type fakeAvailabilitySetter struct {
	gotStatus model.MemberStatus
	gotReturn *time.Time
	err       error
}

func (f *fakeAvailabilitySetter) SetAvailability(_ context.Context, _ uuid.UUID, status model.MemberStatus, returnDate *time.Time) error {
	f.gotStatus = status
	f.gotReturn = returnDate

	return f.err
}

type fakeActivationSetter struct {
	gotStatus model.MemberStatus
}

func (f *fakeActivationSetter) SetActivation(_ context.Context, _ uuid.UUID, status model.MemberStatus) error {
	f.gotStatus = status

	return nil
}

type fakeMemberAccounts struct {
	accountID uuid.UUID
	ok        bool
}

func (f *fakeMemberAccounts) AccountID(_ context.Context, _ uuid.UUID) (uuid.UUID, bool, error) {
	return f.accountID, f.ok, nil
}

type fakeOrgAdmins struct {
	role       model.OrgRole
	adminCount int
	added      *model.OrgMembership
}

func (f *fakeOrgAdmins) RoleFor(_ context.Context, _, _ uuid.UUID) (model.OrgRole, error) {
	return f.role, nil
}

func (f *fakeOrgAdmins) AdminCount(_ context.Context, _ uuid.UUID) (int, error) {
	return f.adminCount, nil
}

func (f *fakeOrgAdmins) AddMember(_ context.Context, m model.OrgMembership) error {
	f.added = &m

	return nil
}

// --- availability ---

func TestSetMemberAvailability_RejectsBadStatus(t *testing.T) {
	t.Parallel()

	h := MustNewSetMemberAvailabilityHandler(&fakeAvailabilitySetter{})

	err := h.Handle(context.Background(), SetMemberAvailabilityCommand{MemberID: uuid.New(), Status: model.MemberStatusDeactivated})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("want InvalidError for a non-availability status, got %v", err)
	}
}

func TestSetMemberAvailability_ClearsReturnDateWhenActive(t *testing.T) {
	t.Parallel()

	setter := &fakeAvailabilitySetter{}
	h := MustNewSetMemberAvailabilityHandler(setter)
	when := time.Now()

	if err := h.Handle(context.Background(), SetMemberAvailabilityCommand{
		MemberID: uuid.New(), Status: model.MemberStatusActive, ReturnDate: &when,
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if setter.gotStatus != model.MemberStatusActive || setter.gotReturn != nil {
		t.Errorf("active must clear the return date, got status=%q return=%v", setter.gotStatus, setter.gotReturn)
	}
}

// --- activation ---

func TestSetMemberActivation_MapsStatus(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		active bool
		want   model.MemberStatus
	}{
		{true, model.MemberStatusActive},
		{false, model.MemberStatusDeactivated},
	} {
		setter := &fakeActivationSetter{}
		h := MustNewSetMemberActivationHandler(setter)

		if err := h.Handle(context.Background(), SetMemberActivationCommand{MemberID: uuid.New(), Active: tc.active}); err != nil {
			t.Fatalf("Handle: %v", err)
		}

		if setter.gotStatus != tc.want {
			t.Errorf("active=%v: status = %q, want %q", tc.active, setter.gotStatus, tc.want)
		}
	}
}

// --- org admin ---

func TestSetOrgAdmin_RefusesExternalMember(t *testing.T) {
	t.Parallel()

	h := MustNewSetOrgAdminHandler(&fakeMemberAccounts{ok: false}, &fakeOrgAdmins{})

	err := h.Handle(context.Background(), SetOrgAdminCommand{OrgID: uuid.New(), MemberID: uuid.New(), IsAdmin: true})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("want InvalidError for an external member, got %v", err)
	}
}

func TestSetOrgAdmin_RefusesRemovingLastAdmin(t *testing.T) {
	t.Parallel()

	orgs := &fakeOrgAdmins{role: model.OrgRoleAdmin, adminCount: 1}
	h := MustNewSetOrgAdminHandler(&fakeMemberAccounts{accountID: uuid.New(), ok: true}, orgs)

	err := h.Handle(context.Background(), SetOrgAdminCommand{OrgID: uuid.New(), MemberID: uuid.New(), IsAdmin: false})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("want InvalidError removing the last admin, got %v", err)
	}

	if orgs.added != nil {
		t.Error("nothing should be written when the guard trips")
	}
}

func TestSetOrgAdmin_GrantsAndRevokes(t *testing.T) {
	t.Parallel()

	// Grant.
	orgs := &fakeOrgAdmins{}
	h := MustNewSetOrgAdminHandler(&fakeMemberAccounts{accountID: uuid.New(), ok: true}, orgs)

	if err := h.Handle(context.Background(), SetOrgAdminCommand{OrgID: uuid.New(), MemberID: uuid.New(), IsAdmin: true}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	if orgs.added == nil || orgs.added.Role != model.OrgRoleAdmin {
		t.Errorf("grant must upsert an admin role, got %+v", orgs.added)
	}

	// Revoke one of several admins.
	orgs = &fakeOrgAdmins{role: model.OrgRoleAdmin, adminCount: 2}
	h = MustNewSetOrgAdminHandler(&fakeMemberAccounts{accountID: uuid.New(), ok: true}, orgs)

	if err := h.Handle(context.Background(), SetOrgAdminCommand{OrgID: uuid.New(), MemberID: uuid.New(), IsAdmin: false}); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if orgs.added == nil || orgs.added.Role != model.OrgRoleMember {
		t.Errorf("revoke must upsert a member role, got %+v", orgs.added)
	}
}
