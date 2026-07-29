// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/edition"
	"github.com/orako-io/core/internal/pkg/errs"
)

// errCounter fails every count, exercising the fail-closed paths. It satisfies
// both the CountAll and CountByOrg shapes.
type errCounter struct{}

func (errCounter) CountAll(context.Context) (int, error) {
	return 0, errors.New("db down")
}

func (errCounter) CountByOrg(context.Context, uuid.UUID) (int, error) {
	return 0, errors.New("db down")
}

// fakeOrgCounter counts instance organizations for the org-cap gate.
type fakeOrgCounter struct{ count int }

func (c fakeOrgCounter) CountAll(context.Context) (int, error) { return c.count, nil }

type orderedLimitStore struct {
	events []string
}

func (s *orderedLimitStore) LockInstance(context.Context) error {
	s.events = append(s.events, "lock")

	return nil
}

func (s *orderedLimitStore) CountAll(context.Context) (int, error) {
	s.events = append(s.events, "count")

	return 0, nil
}

// TestCommunityMemberGate covers the 5-member cap: allowed below the cap,
// blocked at/above it, and never gated for a project without an org.
func TestCommunityMemberGate(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()

	tests := map[string]struct {
		usage     int
		orgOnProj uuid.UUID
		wantBlock bool
	}{
		"below cap":       {usage: 4, orgOnProj: orgID, wantBlock: false},
		"at cap":          {usage: 5, orgOnProj: orgID, wantBlock: true},
		"above cap":       {usage: 9, orgOnProj: orgID, wantBlock: true},
		"no org on proj":  {usage: 99, orgOnProj: uuid.Nil, wantBlock: false},
		"one below empty": {usage: 0, orgOnProj: orgID, wantBlock: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			gate := CommunityMemberGate{
				projects: gateProjectReader{orgID: tc.orgOnProj},
				members:  gateSeatCounter{count: tc.usage},
				max:      5,
			}

			err := gate.AllowNewMember(context.Background(), uuid.New())
			assertBlocked(t, err, tc.wantBlock)
		})
	}
}

// TestCommunityOrgGate covers the 1-org cap.
func TestCommunityOrgGate(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		count     int
		wantBlock bool
	}{
		"empty instance": {count: 0, wantBlock: false},
		"at cap":         {count: 1, wantBlock: true},
		"above cap":      {count: 3, wantBlock: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			gate := CommunityOrgGate{orgs: fakeOrgCounter{count: tc.count}, max: 1}
			assertBlocked(t, gate.AllowNewOrg(context.Background()), tc.wantBlock)
		})
	}
}

func TestCommunityOrgGateLocksBeforeCounting(t *testing.T) {
	t.Parallel()

	store := &orderedLimitStore{}
	gate := CommunityOrgGate{orgs: store, locker: store, max: 1}

	if err := gate.AllowNewOrg(t.Context()); err != nil {
		t.Fatalf("AllowNewOrg: %v", err)
	}

	if len(store.events) != 2 || store.events[0] != "lock" || store.events[1] != "count" {
		t.Fatalf("events = %v, want [lock count]", store.events)
	}
}

// TestCommunityProjectGate covers the 1-project cap, including the nil-org
// short-circuit.
func TestCommunityProjectGate(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()

	tests := map[string]struct {
		count     int
		orgID     uuid.UUID
		wantBlock bool
	}{
		"below cap":  {count: 0, orgID: orgID, wantBlock: false},
		"at cap":     {count: 1, orgID: orgID, wantBlock: true},
		"nil org id": {count: 99, orgID: uuid.Nil, wantBlock: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			gate := CommunityProjectGate{projects: gateSeatCounter{count: tc.count}, max: 1}
			assertBlocked(t, gate.AllowNewProject(context.Background(), tc.orgID), tc.wantBlock)
		})
	}
}

// TestBuildGatesSelectByEdition guards the prod-safety keystone: the build
// functions must select the right gate per edition. A regression here (e.g. a
// reordered switch) could silently cap the paying SaaS at 5 members, so assert
// the concrete returned type for each edition.
func TestBuildGatesSelectByEdition(t *testing.T) {
	t.Parallel()

	proj := gateProjectReader{orgID: uuid.New()}

	saas := edition.Edition{Kind: edition.SaaS}
	community := edition.Edition{Kind: edition.Community, Limits: edition.CommunityLimits}
	licensed := edition.Edition{Kind: edition.Licensed, Limits: edition.Limits{MaxMembers: 25, MaxOrgs: 5, MaxProjects: 50}}

	// Member gate: SaaS → billing seat gate, Community → community cap, Licensed
	// → nil (unenforced, honor system).
	if g := BuildMemberGate(proj, gateSeatCounter{}, fakeInstanceSeats{}, nil, nil, saas); g != nil {
		t.Errorf("SaaS member gate: want nil (injected by the SaaS build), got %T", g)
	}

	if _, ok := BuildMemberGate(proj, gateSeatCounter{}, fakeInstanceSeats{}, nil, nil, community).(CommunityMemberGate); !ok {
		t.Errorf("Community member gate: want CommunityMemberGate, got %T", BuildMemberGate(proj, gateSeatCounter{}, fakeInstanceSeats{}, nil, nil, community))
	}

	if _, ok := BuildMemberGate(proj, gateSeatCounter{}, fakeInstanceSeats{}, nil, nil, licensed).(LicensedSeatGate); !ok {
		t.Errorf("Licensed member gate: want LicensedSeatGate (instance-wide), got %T", BuildMemberGate(proj, gateSeatCounter{}, fakeInstanceSeats{}, nil, nil, licensed))
	}

	// Org gate: only Community.
	if g := BuildOrgGate(fakeOrgCounter{}, nil, saas); g != nil {
		t.Errorf("SaaS org gate: want nil, got %T", g)
	}

	if _, ok := BuildOrgGate(fakeOrgCounter{}, nil, community).(CommunityOrgGate); !ok {
		t.Errorf("Community org gate: want CommunityOrgGate, got %T", BuildOrgGate(fakeOrgCounter{}, nil, community))
	}

	if _, ok := BuildOrgGate(fakeOrgCounter{}, nil, licensed).(CommunityOrgGate); !ok {
		t.Errorf("Licensed org gate: want an org gate (token MaxOrgs), got %T", BuildOrgGate(fakeOrgCounter{}, nil, licensed))
	}

	// Project gate: only Community.
	if g := BuildProjectGate(gateSeatCounter{}, nil, saas); g != nil {
		t.Errorf("SaaS project gate: want nil, got %T", g)
	}

	if _, ok := BuildProjectGate(gateSeatCounter{}, nil, community).(CommunityProjectGate); !ok {
		t.Errorf("Community project gate: want CommunityProjectGate, got %T", BuildProjectGate(gateSeatCounter{}, nil, community))
	}

	if _, ok := BuildProjectGate(gateSeatCounter{}, nil, licensed).(CommunityProjectGate); !ok {
		t.Errorf("Licensed project gate: want a project gate (token MaxProjects), got %T", BuildProjectGate(gateSeatCounter{}, nil, licensed))
	}
}

// TestGatesFailClosedOnCountError proves a count failure denies (never allows
// past the cap).
func TestGatesFailClosedOnCountError(t *testing.T) {
	t.Parallel()

	ec := errCounter{}

	if err := (CommunityMemberGate{projects: gateProjectReader{orgID: uuid.New()}, members: ec, max: 5}).
		AllowNewMember(context.Background(), uuid.New()); err == nil {
		t.Error("member gate must deny on count error, got nil")
	}

	if err := (CommunityOrgGate{orgs: ec, max: 1}).AllowNewOrg(context.Background()); err == nil {
		t.Error("org gate must deny on count error, got nil")
	}

	if err := (CommunityProjectGate{projects: ec, max: 1}).
		AllowNewProject(context.Background(), uuid.New()); err == nil {
		t.Error("project gate must deny on count error, got nil")
	}
}

// fakeInstanceSeats is the instance-wide seat counter for the licensed gate.
type fakeInstanceSeats struct{ count int }

func (f fakeInstanceSeats) CountInstanceSeats(context.Context) (int, error) { return f.count, nil }

// TestLicensedSeatGate covers the instance-wide seat cap of a paid license.
func TestLicensedSeatGate(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		count, max int
		wantBlock  bool
	}{
		"below cap": {count: 24, max: 25, wantBlock: false},
		"at cap":    {count: 25, max: 25, wantBlock: true},
		"above cap": {count: 99, max: 25, wantBlock: true},
		"unlimited": {count: 999, max: 0, wantBlock: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			gate := LicensedSeatGate{seats: fakeInstanceSeats{count: tc.count}, max: tc.max}
			assertBlocked(t, gate.AllowNewMember(context.Background(), uuid.New()), tc.wantBlock)
		})
	}
}

// assertBlocked asserts the gate result: a block must be a typed InvalidError
// (rendered as the upgrade prompt); an allow must be nil.
func assertBlocked(t *testing.T, err error, wantBlock bool) {
	t.Helper()

	if wantBlock {
		var inv errs.InvalidError
		if !errors.As(err, &inv) {
			t.Fatalf("expected errs.InvalidError, got %T: %v", err, err)
		}

		return
	}

	if err != nil {
		t.Fatalf("expected the cap to allow, got %v", err)
	}
}

// gateProjectReader is a projectOrgReader fake for the gate tests.
type gateProjectReader struct {
	orgID uuid.UUID
	err   error
}

func (r gateProjectReader) ByID(_ context.Context, id uuid.UUID) (model.Project, error) {
	if r.err != nil {
		return model.Project{}, r.err
	}

	return model.Project{ID: id, Name: "p", OrgID: r.orgID}, nil
}

// gateSeatCounter is an orgMemberCounter fake for the gate tests.
type gateSeatCounter struct{ count int }

func (c gateSeatCounter) CountByOrg(_ context.Context, _ uuid.UUID) (int, error) {
	return c.count, nil
}
