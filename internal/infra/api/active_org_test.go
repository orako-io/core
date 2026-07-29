// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
)

// fakeScoper is a stub ActiveOrgScoper for applyActiveOrg tests.
type fakeScoper struct {
	scoped CallerIdentity
	ok     bool
	err    error
	called bool
}

func (f *fakeScoper) Scope(_ context.Context, identity CallerIdentity, _ uuid.UUID) (CallerIdentity, bool, error) {
	f.called = true
	if f.err != nil {
		return identity, false, f.err
	}

	return f.scoped, f.ok, nil
}

func TestApplyActiveOrg(t *testing.T) {
	t.Parallel()

	current := uuid.New()
	target := uuid.New()
	base := CallerIdentity{MemberID: uuid.New(), ProjectID: uuid.New(), OrgID: current, Role: model.RoleAdmin}
	scopedIdentity := CallerIdentity{MemberID: base.MemberID, ProjectID: uuid.New(), OrgID: target, IsOrgAdmin: true}

	t.Run("blank header leaves identity unchanged and does not call scoper", func(t *testing.T) {
		t.Parallel()

		scoper := &fakeScoper{scoped: scopedIdentity, ok: true}

		got, err := applyActiveOrg(context.Background(), scoper, base, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if got.OrgID != current || scoper.called {
			t.Fatalf("expected unchanged identity and no scoper call, got org=%v called=%v", got.OrgID, scoper.called)
		}
	})

	t.Run("unparsable header is rejected", func(t *testing.T) {
		t.Parallel()

		scoper := &fakeScoper{scoped: scopedIdentity, ok: true}

		_, err := applyActiveOrg(context.Background(), scoper, base, "not-a-uuid")
		if connect.CodeOf(err) != connect.CodeInvalidArgument || scoper.called {
			t.Fatalf("expected CodeInvalidArgument without scoper call, got called=%v err=%v", scoper.called, err)
		}
	})

	t.Run("non-empty whitespace header is rejected", func(t *testing.T) {
		t.Parallel()

		scoper := &fakeScoper{scoped: scopedIdentity, ok: true}

		_, err := applyActiveOrg(context.Background(), scoper, base, "   ")
		if connect.CodeOf(err) != connect.CodeInvalidArgument || scoper.called {
			t.Fatalf("expected CodeInvalidArgument without scoper call, got called=%v err=%v", scoper.called, err)
		}
	})

	t.Run("header equal to current org is ignored", func(t *testing.T) {
		t.Parallel()

		scoper := &fakeScoper{scoped: scopedIdentity, ok: true}

		got, err := applyActiveOrg(context.Background(), scoper, base, current.String())
		if err != nil || got.OrgID != current || scoper.called {
			t.Fatalf("expected ignore, got org=%v called=%v err=%v", got.OrgID, scoper.called, err)
		}
	})

	t.Run("participated org is applied", func(t *testing.T) {
		t.Parallel()

		scoper := &fakeScoper{scoped: scopedIdentity, ok: true}

		got, err := applyActiveOrg(context.Background(), scoper, base, target.String())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if got.OrgID != target || !got.IsOrgAdmin {
			t.Fatalf("expected scoped-to-target identity, got %+v", got)
		}
	})

	t.Run("non-participated org is rejected", func(t *testing.T) {
		t.Parallel()

		scoper := &fakeScoper{ok: false}

		_, err := applyActiveOrg(context.Background(), scoper, base, target.String())
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("expected CodePermissionDenied, got %v", err)
		}
	})

	t.Run("scoper error surfaces as internal", func(t *testing.T) {
		t.Parallel()

		scoper := &fakeScoper{err: errors.New("db down")}

		_, err := applyActiveOrg(context.Background(), scoper, base, target.String())
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("expected CodeInternal, got %v", err)
		}
	})
}

// fakeOrgProjectResolver / fakeOrgRoleReader stub DBActiveOrgScoper's ports.
type fakeOrgProjectResolver struct {
	projectID uuid.UUID
	// memberID is what the ACCOUNT-keyed lookup resolves: the caller's own
	// member row in the target org.
	memberID uuid.UUID
	ok       bool
	err      error
}

func (f fakeOrgProjectResolver) ProjectInOrgForMember(_ context.Context, _, _ uuid.UUID) (uuid.UUID, bool, error) {
	return f.projectID, f.ok, f.err
}

func (f fakeOrgProjectResolver) ProjectInOrgForAccount(_ context.Context, _, _ uuid.UUID) (uuid.UUID, uuid.UUID, bool, error) {
	return f.projectID, f.memberID, f.ok, f.err
}

// fakeScopedMemberships stubs the scoped member's project set.
type fakeScopedMemberships struct {
	projects []repository.ProjectWithRole
	err      error
}

func (f fakeScopedMemberships) ProjectsByMember(_ context.Context, _ uuid.UUID) ([]repository.ProjectWithRole, error) {
	return f.projects, f.err
}

type fakeOrgRoleReader struct {
	role model.OrgRole
	err  error
}

func (f fakeOrgRoleReader) RoleFor(_ context.Context, _, _ uuid.UUID) (model.OrgRole, error) {
	return f.role, f.err
}

func TestDBActiveOrgScoper(t *testing.T) {
	t.Parallel()

	target := uuid.New()
	newProject := uuid.New()
	account := uuid.New()

	t.Run("member not in org is not scoped", func(t *testing.T) {
		t.Parallel()

		s := NewDBActiveOrgScoper(fakeOrgProjectResolver{ok: false}, fakeOrgRoleReader{}, fakeScopedMemberships{})
		id := CallerIdentity{MemberID: uuid.New(), OrgID: uuid.New()}

		got, ok, err := s.Scope(context.Background(), id, target)
		if err != nil || ok || got.OrgID != id.OrgID {
			t.Fatalf("expected not scoped, got ok=%v org=%v err=%v", ok, got.OrgID, err)
		}
	})

	t.Run("dev stub keeps its token admin flag", func(t *testing.T) {
		t.Parallel()

		s := NewDBActiveOrgScoper(fakeOrgProjectResolver{projectID: newProject, ok: true}, fakeOrgRoleReader{},
			fakeScopedMemberships{projects: []repository.ProjectWithRole{{ID: newProject}}})
		id := CallerIdentity{MemberID: uuid.New(), OrgID: uuid.New(), IsOrgAdmin: true} // AccountID nil

		got, ok, err := s.Scope(context.Background(), id, target)
		if err != nil || !ok {
			t.Fatalf("expected scoped, got ok=%v err=%v", ok, err)
		}

		if got.OrgID != target || got.ProjectID != newProject || !got.IsOrgAdmin {
			t.Fatalf("expected target+project+admin, got %+v", got)
		}
	})

	t.Run("account admin in target org gets admin", func(t *testing.T) {
		t.Parallel()

		s := NewDBActiveOrgScoper(
			fakeOrgProjectResolver{projectID: newProject, ok: true},
			fakeOrgRoleReader{role: model.OrgRoleAdmin},
			fakeScopedMemberships{projects: []repository.ProjectWithRole{{ID: newProject}}},
		)
		id := CallerIdentity{AccountID: account, MemberID: uuid.New(), OrgID: uuid.New(), IsOrgAdmin: false}

		got, ok, err := s.Scope(context.Background(), id, target)
		if err != nil || !ok || !got.IsOrgAdmin {
			t.Fatalf("expected admin in target, got ok=%v admin=%v err=%v", ok, got.IsOrgAdmin, err)
		}
	})

	t.Run("account member in target org is not admin", func(t *testing.T) {
		t.Parallel()

		s := NewDBActiveOrgScoper(
			fakeOrgProjectResolver{projectID: newProject, ok: true},
			fakeOrgRoleReader{role: model.OrgRoleMember},
			fakeScopedMemberships{projects: []repository.ProjectWithRole{{ID: newProject}}},
		)
		id := CallerIdentity{AccountID: account, MemberID: uuid.New(), OrgID: uuid.New(), IsOrgAdmin: true}

		got, ok, err := s.Scope(context.Background(), id, target)
		if err != nil || !ok || got.IsOrgAdmin {
			t.Fatalf("expected non-admin in target, got ok=%v admin=%v err=%v", ok, got.IsOrgAdmin, err)
		}
	})
}

// TestDBActiveOrgScoper_AccountSwitchRescopesMember is the org-switch bug fix
// (2026-07-13): each org has its OWN member row per account, so switching orgs
// must swap the identity's member id and project set to the TARGET org's —
// keying the check on the token member silently refused every switch to a
// newly created org ("creating an organization does nothing").
func TestDBActiveOrgScoper_AccountSwitchRescopesMember(t *testing.T) {
	t.Parallel()

	target := uuid.New()
	tokenMember := uuid.New()
	targetMember := uuid.New()
	targetProject := uuid.New()

	s := NewDBActiveOrgScoper(
		fakeOrgProjectResolver{projectID: targetProject, memberID: targetMember, ok: true},
		fakeOrgRoleReader{role: model.OrgRoleAdmin},
		fakeScopedMemberships{projects: []repository.ProjectWithRole{{ID: targetProject}}},
	)

	id := CallerIdentity{
		AccountID:  uuid.New(),
		MemberID:   tokenMember,
		OrgID:      uuid.New(),
		ProjectID:  uuid.New(),
		ProjectIDs: []uuid.UUID{uuid.New()},
	}

	got, ok, err := s.Scope(context.Background(), id, target)
	if err != nil || !ok {
		t.Fatalf("expected scoped, got ok=%v err=%v", ok, err)
	}

	if got.MemberID != targetMember {
		t.Errorf("MemberID = %v, want the target org's member row %v (not the token's %v)", got.MemberID, targetMember, tokenMember)
	}

	if got.ProjectID != targetProject || len(got.ProjectIDs) != 1 || got.ProjectIDs[0] != targetProject {
		t.Errorf("project scope = %v/%v, want the target org's project set", got.ProjectID, got.ProjectIDs)
	}

	if got.OrgID != target || !got.IsOrgAdmin {
		t.Errorf("org scope = %v admin=%v, want target org with recomputed admin", got.OrgID, got.IsOrgAdmin)
	}
}
