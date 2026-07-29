// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// fakeOrgMemberReader is a stand-in OrgMemberReader that returns fixed roster
// and pending slices, recording whether the pending path was consulted.
type fakeOrgMemberReader struct {
	roster        []OrgMemberView
	pending       []OrgMemberView
	pendingCalled bool
}

func (f *fakeOrgMemberReader) ListOrgMembers(_ context.Context, _ uuid.UUID) ([]OrgMemberView, error) {
	return f.roster, nil
}

func (f *fakeOrgMemberReader) ListPendingOrgMembers(_ context.Context, _ uuid.UUID) ([]OrgMemberView, error) {
	f.pendingCalled = true
	return f.pending, nil
}

func (f *fakeOrgMemberReader) OrgMemberByID(_ context.Context, _, _ uuid.UUID) (OrgMemberView, error) {
	return OrgMemberView{}, nil
}

// TestListMembers_RosterExcludesPendingByDefault proves that without
// IncludePending (the non-admin path) the handler returns ONLY the roster and
// never even consults the pending list — a non-admin can't read a pending member.
func TestListMembers_RosterExcludesPendingByDefault(t *testing.T) {
	t.Parallel()

	active := uuid.New()
	reader := &fakeOrgMemberReader{
		roster:  []OrgMemberView{{MemberID: active, Status: "active"}},
		pending: []OrgMemberView{{MemberID: uuid.New(), Status: "pending"}},
	}

	h := MustNewListMembersHandler(reader)

	out, err := h.Handle(t.Context(), ListMembersQuery{OrgID: uuid.New()})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if reader.pendingCalled {
		t.Error("pending list must NOT be consulted when IncludePending is false")
	}

	if len(out) != 1 || out[0].MemberID != active {
		t.Fatalf("out = %+v, want only the active roster member", out)
	}

	for _, m := range out {
		if m.Status == "pending" {
			t.Errorf("pending member %v leaked into the default roster", m.MemberID)
		}
	}
}

// TestListMembers_IncludePendingAppendsPending proves that the admin path
// (IncludePending) appends the pending members to the roster.
func TestListMembers_IncludePendingAppendsPending(t *testing.T) {
	t.Parallel()

	active := uuid.New()
	pending := uuid.New()
	reader := &fakeOrgMemberReader{
		roster:  []OrgMemberView{{MemberID: active, Status: "active"}},
		pending: []OrgMemberView{{MemberID: pending, Status: "pending"}},
	}

	h := MustNewListMembersHandler(reader)

	out, err := h.Handle(t.Context(), ListMembersQuery{OrgID: uuid.New(), IncludePending: true})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if !reader.pendingCalled {
		t.Error("pending list must be consulted when IncludePending is true")
	}

	byID := map[uuid.UUID]OrgMemberView{}
	for _, m := range out {
		byID[m.MemberID] = m
	}

	if _, ok := byID[active]; !ok {
		t.Error("active roster member missing from admin view")
	}

	if got, ok := byID[pending]; !ok || got.Status != "pending" {
		t.Errorf("pending member not appended for admin view: %+v", out)
	}
}
