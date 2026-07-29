// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/auth"
)

type fakeMemberByEmail struct {
	member model.Member
	err    error
}

func (f fakeMemberByEmail) ByEmail(_ context.Context, _ string) (model.Member, error) {
	return f.member, f.err
}

func (f fakeMemberByEmail) Update(_ context.Context, _ model.Member) error {
	return nil
}

// fakeMemberByID stubs memberByIDAuthReader (resolving a member already
// authenticated by a token, e.g. the mcp-token and human-authenticator paths).
type fakeMemberByID struct{ member model.Member }

func (f *fakeMemberByID) ByID(_ context.Context, _ uuid.UUID) (model.Member, error) {
	return f.member, nil
}

// fakeFallback is a generic Authenticator stub recording whether it was
// reached, for tests asserting a wrapper authenticator falls through to it.
type fakeFallback struct{ called bool }

func (f *fakeFallback) Authenticate(_ context.Context, _ string) (CallerIdentity, error) {
	f.called = true

	return CallerIdentity{}, nil
}

func (f *fakeFallback) AuthenticateAccount(_ context.Context, _ string) (CallerIdentity, error) {
	f.called = true

	return CallerIdentity{}, nil
}

type fakeMemberByAccount struct {
	member model.Member
	err    error
}

func (f fakeMemberByAccount) ByAccountID(_ context.Context, _ uuid.UUID) (model.Member, error) {
	return f.member, f.err
}

// notFoundByAccount is the common case where the caller's member is not linked by
// account_id, forcing the resolver onto the email fallback.
var notFoundByAccount = fakeMemberByAccount{err: adaptererr.ErrNotFound}

type fakeMemberships struct {
	rows []repository.ProjectWithRole
	err  error
}

func (f fakeMemberships) ProjectsByMember(_ context.Context, _ uuid.UUID) ([]repository.ProjectWithRole, error) {
	return f.rows, f.err
}

// fakeOrgAdmin stubs the org-admin lookup. role is returned for any (org,account);
// err (e.g. adaptererr.ErrNotFound) simulates a non-member account.
type fakeOrgAdmin struct {
	role model.OrgRole
	err  error
}

func (f fakeOrgAdmin) RoleFor(_ context.Context, _, _ uuid.UUID) (model.OrgRole, error) {
	return f.role, f.err
}

// notOrgMember is the common case: the account is not in org_members, so RoleFor
// reports ErrNotFound and the resolver must treat the caller as a non-admin.
var notOrgMember = fakeOrgAdmin{err: adaptererr.ErrNotFound}

// fakeAccountStore supports the resolver's find-or-create flow.
type fakeAccountStore struct {
	bySubject map[string]model.Account
	byEmail   map[string]model.Account
	created   []model.Account
	createErr error
}

func newFakeAccountStore() *fakeAccountStore {
	return &fakeAccountStore{
		bySubject: map[string]model.Account{},
		byEmail:   map[string]model.Account{},
	}
}

func (f *fakeAccountStore) seed(acc model.Account) {
	f.byEmail[acc.Email] = acc
	if acc.Subject != "" {
		f.bySubject[acc.Subject] = acc
	}
}

func (f *fakeAccountStore) BySubject(_ context.Context, subject string) (model.Account, error) {
	if a, ok := f.bySubject[subject]; ok {
		return a, nil
	}

	return model.Account{}, adaptererr.ErrNotFound
}

func (f *fakeAccountStore) ByEmail(_ context.Context, email string) (model.Account, error) {
	if a, ok := f.byEmail[email]; ok {
		return a, nil
	}

	return model.Account{}, adaptererr.ErrNotFound
}

func (f *fakeAccountStore) Create(_ context.Context, acc model.Account) error {
	if f.createErr != nil {
		return f.createErr
	}

	f.created = append(f.created, acc)
	f.seed(acc)

	return nil
}

func TestDBPrincipalResolver_ResolvesIdentityFromTables(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	projectID := uuid.New()
	orgID := uuid.New()
	accountID := uuid.New()

	accounts := newFakeAccountStore()
	accounts.seed(model.Account{ID: accountID, Email: "sarah@example.com", Subject: "sub-1"})

	r := NewDBPrincipalResolver(
		accounts,
		fakeMemberByEmail{member: model.Member{ID: memberID, Email: "sarah@example.com", Status: model.MemberStatusActive}},
		notFoundByAccount,
		fakeMemberships{rows: []repository.ProjectWithRole{{ID: projectID, Name: "Acme", OrgID: orgID}}},
		notOrgMember, // non-admin
	)

	id, err := r.Resolve(context.Background(), auth.Claims{Subject: "sub-1", Email: "sarah@example.com"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if id.AccountID != accountID || id.MemberID != memberID || id.ProjectID != projectID || id.OrgID != orgID {
		t.Errorf("identity = %+v; want account/member/project/org resolved from the tables", id)
	}

	// The project role is no longer an authorization source and is not sourced
	// from the membership; a non-org-member is not an admin.
	if id.IsOrgAdmin {
		t.Errorf("IsOrgAdmin = true; a non-org-member must not be an admin")
	}

	if len(accounts.created) != 0 {
		t.Errorf("existing account should not be re-created, got %d creates", len(accounts.created))
	}
}

// TestDBPrincipalResolver_OrgAdminFromTables proves org-admin status is read from
// org_members (RoleFor), never the token: an account whose org role is admin
// resolves to IsOrgAdmin=true with the org set.
func TestDBPrincipalResolver_OrgAdminFromTables(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	projectID := uuid.New()
	orgID := uuid.New()
	accountID := uuid.New()

	accounts := newFakeAccountStore()
	accounts.seed(model.Account{ID: accountID, Email: "admin@example.com", Subject: "sub-a"})

	r := NewDBPrincipalResolver(
		accounts,
		fakeMemberByEmail{err: errors.New("ByEmail must not be reached for an account-linked member")},
		fakeMemberByAccount{member: model.Member{ID: memberID, Status: model.MemberStatusActive}},
		fakeMemberships{rows: []repository.ProjectWithRole{{ID: projectID, Name: "global", OrgID: orgID}}},
		fakeOrgAdmin{role: model.OrgRoleAdmin},
	)

	id, err := r.Resolve(context.Background(), auth.Claims{Subject: "sub-a", Email: "admin@example.com"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if !id.IsOrgAdmin {
		t.Errorf("IsOrgAdmin = false; the org-admin role from RoleFor must set it")
	}

	if id.OrgID != orgID {
		t.Errorf("OrgID = %s, want %s", id.OrgID, orgID)
	}
}

// TestDBPrincipalResolver_OrgMemberIsNotAdmin proves a non-admin org role yields
// IsOrgAdmin=false even though the account is an org member.
func TestDBPrincipalResolver_OrgMemberIsNotAdmin(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	projectID := uuid.New()
	orgID := uuid.New()
	accountID := uuid.New()

	accounts := newFakeAccountStore()
	accounts.seed(model.Account{ID: accountID, Email: "member@example.com", Subject: "sub-m"})

	r := NewDBPrincipalResolver(
		accounts,
		fakeMemberByEmail{member: model.Member{ID: memberID, Email: "member@example.com", Status: model.MemberStatusActive}},
		notFoundByAccount,
		fakeMemberships{rows: []repository.ProjectWithRole{{ID: projectID, Name: "Acme", OrgID: orgID}}},
		fakeOrgAdmin{role: model.OrgRoleMember},
	)

	id, err := r.Resolve(context.Background(), auth.Claims{Subject: "sub-m", Email: "member@example.com"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if id.IsOrgAdmin {
		t.Errorf("IsOrgAdmin = true; an org member (non-admin) must not be an admin")
	}
}

// TestDBPrincipalResolver_OrgAdminLookupErrorSurfaces proves a store error (other
// than ErrNotFound) from RoleFor is surfaced, not swallowed into a non-admin.
func TestDBPrincipalResolver_OrgAdminLookupErrorSurfaces(t *testing.T) {
	t.Parallel()

	accounts := newFakeAccountStore()
	accounts.seed(model.Account{ID: uuid.New(), Email: "boom@example.com", Subject: "sub-b"})

	r := NewDBPrincipalResolver(
		accounts,
		fakeMemberByEmail{member: model.Member{ID: uuid.New(), Email: "boom@example.com", Status: model.MemberStatusActive}},
		notFoundByAccount,
		fakeMemberships{rows: []repository.ProjectWithRole{{ID: uuid.New(), Name: "Acme", OrgID: uuid.New()}}},
		fakeOrgAdmin{err: errors.New("boom")},
	)

	if _, err := r.Resolve(context.Background(), auth.Claims{Subject: "sub-b", Email: "boom@example.com"}); err == nil {
		t.Fatal("expected the org-admin lookup error to surface")
	}
}

func TestDBPrincipalResolver_NoEmail(t *testing.T) {
	t.Parallel()

	r := NewDBPrincipalResolver(newFakeAccountStore(), fakeMemberByEmail{}, notFoundByAccount, fakeMemberships{}, notOrgMember)

	if _, err := r.Resolve(context.Background(), auth.Claims{Subject: "sub-1"}); err == nil {
		t.Fatal("expected error when the token carries no email")
	}
}

// A valid identity with no Orako account/member is provisioned (self-serve
// first login), not rejected — the account is created and resolves account-only.
func TestDBPrincipalResolver_ProvisionsAccountOnFirstLogin(t *testing.T) {
	t.Parallel()

	accounts := newFakeAccountStore()

	r := NewDBPrincipalResolver(
		accounts,
		fakeMemberByEmail{err: adaptererr.ErrNotFound},
		notFoundByAccount,
		fakeMemberships{},
		notOrgMember,
	)

	id, err := r.Resolve(context.Background(), auth.Claims{Subject: "sub-new", Email: "new@example.com"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if len(accounts.created) != 1 {
		t.Fatalf("want the account provisioned once, got %d", len(accounts.created))
	}

	if id.AccountID == uuid.Nil {
		t.Error("AccountID should be set after provisioning")
	}

	if id.MemberID != uuid.Nil || id.ProjectID != uuid.Nil || id.Role != model.RoleUnspecified {
		t.Errorf("fresh account must be account-only, got %+v", id)
	}
}

func TestDBPrincipalResolver_ReusesAccountBySubject(t *testing.T) {
	t.Parallel()

	accounts := newFakeAccountStore()
	accountID := uuid.New()
	accounts.seed(model.Account{ID: accountID, Email: "sarah@example.com", Subject: "sub-1"})

	r := NewDBPrincipalResolver(accounts, fakeMemberByEmail{err: adaptererr.ErrNotFound}, notFoundByAccount, fakeMemberships{}, notOrgMember)

	// A new email but the same subject must resolve the existing account.
	id, err := r.Resolve(context.Background(), auth.Claims{Subject: "sub-1", Email: "changed@example.com"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if id.AccountID != accountID {
		t.Errorf("AccountID = %s, want the existing account resolved by subject", id.AccountID)
	}

	if len(accounts.created) != 0 {
		t.Error("should not create a new account when one exists for the subject")
	}
}

func TestDBPrincipalResolver_CreateRaceRefetches(t *testing.T) {
	t.Parallel()

	accounts := newFakeAccountStore()
	winner := model.Account{ID: uuid.New(), Email: "race@example.com", Subject: "sub-r"}
	// Simulate a concurrent winner: Create fails duplicate, but the row is present.
	accounts.byEmail[winner.Email] = winner
	accounts.createErr = adaptererr.ErrDuplicate

	r := NewDBPrincipalResolver(accounts, fakeMemberByEmail{err: adaptererr.ErrNotFound}, notFoundByAccount, fakeMemberships{}, notOrgMember)

	id, err := r.Resolve(context.Background(), auth.Claims{Subject: "sub-r", Email: "race@example.com"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if id.AccountID != winner.ID {
		t.Errorf("AccountID = %s, want the race winner %s", id.AccountID, winner.ID)
	}
}

func TestDBPrincipalResolver_NoMembershipYieldsUnspecified(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	accounts := newFakeAccountStore()
	accounts.seed(model.Account{ID: uuid.New(), Email: "new@example.com"})

	r := NewDBPrincipalResolver(
		accounts,
		fakeMemberByEmail{member: model.Member{ID: memberID, Email: "new@example.com", Status: model.MemberStatusActive}},
		notFoundByAccount,
		fakeMemberships{rows: nil},
		notOrgMember,
	)

	id, err := r.Resolve(context.Background(), auth.Claims{Email: "new@example.com"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if id.MemberID != memberID || id.ProjectID != uuid.Nil || id.Role != model.RoleUnspecified {
		t.Errorf("identity = %+v; want member with no project/role", id)
	}
}

// An org creator's member row is linked only by account_id and carries an empty
// email, so it is reachable solely via ByAccountID. Resolve must find it there and
// attach the primary project + the org-admin status (from the tables, never the
// token).
func TestDBPrincipalResolver_ResolvesMemberByAccountID(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	projectID := uuid.New()
	orgID := uuid.New()
	accountID := uuid.New()

	accounts := newFakeAccountStore()
	accounts.seed(model.Account{ID: accountID, Email: "creator@example.com", Subject: "sub-c"})

	r := NewDBPrincipalResolver(
		accounts,
		// Email lookup must NOT be consulted for an account-linked member; make it
		// error loudly so a regression that falls through to email is caught.
		fakeMemberByEmail{err: errors.New("ByEmail must not be called for an account-linked member")},
		fakeMemberByAccount{member: model.Member{ID: memberID, Status: model.MemberStatusActive}}, // empty email
		fakeMemberships{rows: []repository.ProjectWithRole{{ID: projectID, Name: "global", OrgID: orgID}}},
		fakeOrgAdmin{role: model.OrgRoleAdmin},
	)

	id, err := r.Resolve(context.Background(), auth.Claims{Subject: "sub-c", Email: "creator@example.com"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if id.AccountID != accountID || id.MemberID != memberID || id.ProjectID != projectID {
		t.Errorf("identity = %+v; want member+project resolved by account_id", id)
	}

	if !id.IsOrgAdmin || id.OrgID != orgID {
		t.Errorf("identity = %+v; want org-admin status resolved for the account_id-linked creator", id)
	}
}

// When both lookups would find a (different) member, account_id wins: it is the
// authoritative link for a logged-in human. Email is only a fallback.
func TestDBPrincipalResolver_AccountIDTakesPrecedenceOverEmail(t *testing.T) {
	t.Parallel()

	byAccountID := uuid.New()
	byEmailID := uuid.New()
	accountID := uuid.New()

	accounts := newFakeAccountStore()
	accounts.seed(model.Account{ID: accountID, Email: "dual@example.com", Subject: "sub-d"})

	r := NewDBPrincipalResolver(
		accounts,
		fakeMemberByEmail{member: model.Member{ID: byEmailID, Email: "dual@example.com", Status: model.MemberStatusActive}},
		fakeMemberByAccount{member: model.Member{ID: byAccountID, Status: model.MemberStatusActive}},
		fakeMemberships{rows: nil},
		notOrgMember,
	)

	id, err := r.Resolve(context.Background(), auth.Claims{Subject: "sub-d", Email: "dual@example.com"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if id.MemberID != byAccountID {
		t.Errorf("MemberID = %s, want the account_id member %s (precedence over email)", id.MemberID, byAccountID)
	}
}

// An invited-by-email member has no account_id link yet, so ByAccountID misses and
// Resolve must fall back to ByEmail.
func TestDBPrincipalResolver_FallsBackToEmail(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	projectID := uuid.New()
	accountID := uuid.New()

	accounts := newFakeAccountStore()
	accounts.seed(model.Account{ID: accountID, Email: "invited@example.com", Subject: "sub-i"})

	r := NewDBPrincipalResolver(
		accounts,
		fakeMemberByEmail{member: model.Member{ID: memberID, Email: "invited@example.com", Status: model.MemberStatusActive}},
		notFoundByAccount,
		fakeMemberships{rows: []repository.ProjectWithRole{{ID: projectID, Name: "Acme", OrgID: uuid.Nil}}},
		notOrgMember,
	)

	id, err := r.Resolve(context.Background(), auth.Claims{Subject: "sub-i", Email: "invited@example.com"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if id.MemberID != memberID || id.ProjectID != projectID {
		t.Errorf("identity = %+v; want the invited member resolved via the email fallback", id)
	}
}

// A store error (other than ErrNotFound) on the account lookup must surface, not be
// swallowed into an account-only identity.
func TestDBPrincipalResolver_AccountLookupErrorSurfaces(t *testing.T) {
	t.Parallel()

	accounts := newFakeAccountStore()
	accounts.seed(model.Account{ID: uuid.New(), Email: "err@example.com", Subject: "sub-e"})

	r := NewDBPrincipalResolver(
		accounts,
		fakeMemberByEmail{err: errors.New("ByEmail must not be reached when ByAccountID errors")},
		fakeMemberByAccount{err: errors.New("boom")},
		fakeMemberships{},
		notOrgMember,
	)

	if _, err := r.Resolve(context.Background(), auth.Claims{Subject: "sub-e", Email: "err@example.com"}); err == nil {
		t.Fatal("expected the account-lookup store error to surface")
	}
}

// deadStatuses are the offboarded member statuses that must fail the member-gated
// dashboard/MCP path and be tolerated (account-only) on the /join path.
var deadStatuses = []model.MemberStatus{
	model.MemberStatusRemoved,
	model.MemberStatusPurged,
	model.MemberStatusDeactivated,
	model.MemberStatusSuspended,
}

// TestDBPrincipalResolver_DeadMemberRevokedOnDashboard proves the offboarding
// boundary (H2) is unchanged on the member-gated Resolve path: a genuinely
// offboarded account with no re-join still gets "member access has been revoked".
func TestDBPrincipalResolver_DeadMemberRevokedOnDashboard(t *testing.T) {
	t.Parallel()

	for _, dead := range deadStatuses {
		t.Run(string(dead), func(t *testing.T) {
			t.Parallel()

			accountID := uuid.New()
			accounts := newFakeAccountStore()
			accounts.seed(model.Account{ID: accountID, Email: "gone@example.com", Subject: "sub-g"})

			r := NewDBPrincipalResolver(
				accounts,
				fakeMemberByEmail{err: adaptererr.ErrNotFound},
				fakeMemberByAccount{member: model.Member{ID: uuid.New(), Status: dead}},
				fakeMemberships{},
				notOrgMember,
			)

			_, err := r.Resolve(context.Background(), auth.Claims{Subject: "sub-g", Email: "gone@example.com"})
			if err == nil || err.Error() != "member access has been revoked" {
				t.Fatalf("Resolve: err = %v, want \"member access has been revoked\"", err)
			}
		})
	}
}

// TestDBPrincipalResolver_ResolveAccountToleratesDeadMember proves the /join seam:
// ResolveAccount demotes an offboarded member to an account-only identity
// (MemberID nil) instead of erroring, so the redeem command can provision a fresh
// membership. It must never return the "revoked" error.
func TestDBPrincipalResolver_ResolveAccountToleratesDeadMember(t *testing.T) {
	t.Parallel()

	for _, dead := range deadStatuses {
		t.Run(string(dead), func(t *testing.T) {
			t.Parallel()

			accountID := uuid.New()
			accounts := newFakeAccountStore()
			accounts.seed(model.Account{ID: accountID, Email: "rejoin@example.com", Subject: "sub-r"})

			r := NewDBPrincipalResolver(
				accounts,
				fakeMemberByEmail{err: adaptererr.ErrNotFound},
				fakeMemberByAccount{member: model.Member{ID: uuid.New(), Status: dead}},
				fakeMemberships{},
				notOrgMember,
			)

			id, err := r.ResolveAccount(context.Background(), auth.Claims{Subject: "sub-r", Email: "rejoin@example.com"})
			if err != nil {
				t.Fatalf("ResolveAccount: unexpected error %v (must tolerate a dead member)", err)
			}

			if id.AccountID != accountID {
				t.Errorf("AccountID = %s, want the resolved account %s", id.AccountID, accountID)
			}

			if id.MemberID != uuid.Nil {
				t.Errorf("MemberID = %s, want nil: a dead member is treated as absent", id.MemberID)
			}
		})
	}
}

// TestDBPrincipalResolver_ResolveAccountResolvesLiveMember proves ResolveAccount
// still resolves a LIVE member fully — identical to Resolve — so an
// already-joined caller is unaffected by the account-only tolerance.
func TestDBPrincipalResolver_ResolveAccountResolvesLiveMember(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	projectID := uuid.New()
	accountID := uuid.New()

	accounts := newFakeAccountStore()
	accounts.seed(model.Account{ID: accountID, Email: "live@example.com", Subject: "sub-l"})

	r := NewDBPrincipalResolver(
		accounts,
		fakeMemberByEmail{err: adaptererr.ErrNotFound},
		fakeMemberByAccount{member: model.Member{ID: memberID, Status: model.MemberStatusPending}},
		fakeMemberships{rows: []repository.ProjectWithRole{{ID: projectID, Name: "global", OrgID: uuid.Nil}}},
		notOrgMember,
	)

	id, err := r.ResolveAccount(context.Background(), auth.Claims{Subject: "sub-l", Email: "live@example.com"})
	if err != nil {
		t.Fatalf("ResolveAccount: %v", err)
	}

	if id.MemberID != memberID || id.ProjectID != projectID {
		t.Errorf("identity = %+v; want the live pending member+project resolved", id)
	}
}

// TestDBPrincipalResolver_RejoinedMemberResolvesOnDashboard proves the end-state
// after a re-join: once the account holds a fresh LIVE pending member (ByAccountID
// prefers it over any dead ghost), the member-gated dashboard Resolve path
// resolves that live member — no "revoked".
func TestDBPrincipalResolver_RejoinedMemberResolvesOnDashboard(t *testing.T) {
	t.Parallel()

	pendingID := uuid.New()
	projectID := uuid.New()
	accountID := uuid.New()

	accounts := newFakeAccountStore()
	accounts.seed(model.Account{ID: accountID, Email: "back@example.com", Subject: "sub-b"})

	// ByAccountID returns the live pending member (the store's ordering prefers a
	// live member over the dead ghost the account also owns).
	r := NewDBPrincipalResolver(
		accounts,
		fakeMemberByEmail{err: adaptererr.ErrNotFound},
		fakeMemberByAccount{member: model.Member{ID: pendingID, Status: model.MemberStatusPending}},
		fakeMemberships{rows: []repository.ProjectWithRole{{ID: projectID, Name: "global", OrgID: uuid.Nil}}},
		notOrgMember,
	)

	id, err := r.Resolve(context.Background(), auth.Claims{Subject: "sub-b", Email: "back@example.com"})
	if err != nil {
		t.Fatalf("Resolve after re-join: %v (want the live pending member, not revoked)", err)
	}

	if id.MemberID != pendingID {
		t.Errorf("MemberID = %s, want the fresh pending member %s", id.MemberID, pendingID)
	}
}

// ── jwtAuthenticator ──

type fakeVerifier struct {
	claims auth.Claims
	err    error
}

func (f fakeVerifier) Verify(_ context.Context, _ string) (auth.Claims, error) {
	return f.claims, f.err
}

type fakeResolver struct {
	id  CallerIdentity
	err error
	// accountID / accountErr, when set, are what ResolveAccount returns; they
	// default to id/err so a resolver configured only for Resolve behaves the
	// same on both seams.
	accountID  CallerIdentity `exhaustruct:"optional"`
	accountErr error          `exhaustruct:"optional"`
	accountSet bool           `exhaustruct:"optional"`
}

func (f fakeResolver) Resolve(_ context.Context, _ auth.Claims) (CallerIdentity, error) {
	return f.id, f.err
}

func (f fakeResolver) ResolveAccount(_ context.Context, _ auth.Claims) (CallerIdentity, error) {
	if f.accountSet {
		return f.accountID, f.accountErr
	}

	return f.id, f.err
}

func TestJWTAuthenticator_EndToEnd(t *testing.T) {
	t.Parallel()

	want := CallerIdentity{MemberID: uuid.New(), ProjectID: uuid.New(), Role: model.RoleSpecialist}
	a := NewJWTAuthenticator(
		fakeVerifier{claims: auth.Claims{Subject: "s", Email: "e@x.com"}},
		fakeResolver{id: want},
	)

	got, err := a.Authenticate(context.Background(), "Bearer some.jwt.token")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("identity = %+v, want %+v", got, want)
	}
}

func TestJWTAuthenticator_MissingBearer(t *testing.T) {
	t.Parallel()

	a := NewJWTAuthenticator(fakeVerifier{}, fakeResolver{})

	for _, h := range []string{"", "some.jwt.token", "Bearer "} {
		if _, err := a.Authenticate(context.Background(), h); err == nil {
			t.Errorf("expected error for header %q", h)
		}
	}
}

func TestJWTAuthenticator_VerifierError(t *testing.T) {
	t.Parallel()

	a := NewJWTAuthenticator(fakeVerifier{err: errors.New("bad token")}, fakeResolver{})

	if _, err := a.Authenticate(context.Background(), "Bearer x"); err == nil {
		t.Fatal("expected error when verification fails")
	}
}
