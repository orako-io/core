// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/errs"
)

// ---- fakes ---------------------------------------------------------------

type fakeJoinTokenReader struct {
	token repository.JoinToken
	err   error
}

func (f fakeJoinTokenReader) JoinTokenByToken(_ context.Context, _ string) (repository.JoinToken, error) {
	return f.token, f.err
}

type fakePendingProvisioner struct {
	created     *model.Member
	memberships []repository.ProjectMembership
	createErr   error
}

func (f *fakePendingProvisioner) CreateWithAccount(_ context.Context, m model.Member) error {
	if f.createErr != nil {
		return f.createErr
	}

	f.created = &m

	return nil
}

func (f *fakePendingProvisioner) AddToProject(_ context.Context, m repository.ProjectMembership) (bool, error) {
	f.memberships = append(f.memberships, m)

	return true, nil
}

// fakeMemberByAccount is org-scoped: it returns member only when queried for the
// org the member lives in (inOrg). A zero-value inOrg means "any org" so the
// simpler tests need not wire an org. This mirrors the real ByAccountInOrg, whose
// join is bounded by projects.org_id.
type fakeMemberByAccount struct {
	member model.Member
	inOrg  uuid.UUID `exhaustruct:"optional"`
	found  bool
}

func (f fakeMemberByAccount) ByAccountInOrg(_ context.Context, _, orgID uuid.UUID) (model.Member, error) {
	if !f.found || (f.inOrg != uuid.Nil && orgID != f.inOrg) {
		return model.Member{}, adaptererr.ErrNotFound
	}

	return f.member, nil
}

// ---- tests ---------------------------------------------------------------

func TestRedeemJoinTokenProvisionsPendingFromTokenNotInput(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	projID := uuid.New()
	accountID := uuid.New()

	tokens := fakeJoinTokenReader{token: repository.JoinToken{OrgID: orgID, ProjectID: projID}}
	prov := &fakePendingProvisioner{}
	byAccount := fakeMemberByAccount{found: false}

	h := MustNewRedeemJoinTokenHandler(tokens, prov, byAccount, fakeTransactor{}, nil)

	res, err := h.Handle(context.Background(), RedeemJoinTokenCommand{
		AccountID:   accountID,
		Token:       "some-code",
		Email:       "newbie@example.com",
		DisplayName: "Newbie",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if res.ProjectID != projID {
		t.Errorf("result ProjectID = %s, want the token's project %s (never caller input)", res.ProjectID, projID)
	}

	if prov.created == nil {
		t.Fatal("expected a member to be created")
	}

	if prov.created.Status != model.MemberStatusPending {
		t.Errorf("created member status = %q, want pending", prov.created.Status)
	}

	if prov.created.AccountID != accountID {
		t.Errorf("created member account_id = %s, want %s", prov.created.AccountID, accountID)
	}

	if len(prov.memberships) != 1 {
		t.Fatalf("expected exactly one project membership, got %d", len(prov.memberships))
	}

	// The landing project comes ONLY from the token row.
	if prov.memberships[0].ProjectID != projID {
		t.Errorf("membership project = %s, want the token's project %s", prov.memberships[0].ProjectID, projID)
	}

	if prov.memberships[0].MemberID != prov.created.ID {
		t.Errorf("membership member = %s, want the created member %s", prov.memberships[0].MemberID, prov.created.ID)
	}
}

func TestRedeemJoinTokenRejectsRevoked(t *testing.T) {
	t.Parallel()

	tokens := fakeJoinTokenReader{token: repository.JoinToken{OrgID: uuid.New(), ProjectID: uuid.New(), Revoked: true}}
	prov := &fakePendingProvisioner{}

	h := MustNewRedeemJoinTokenHandler(tokens, prov, fakeMemberByAccount{}, fakeTransactor{}, nil)

	_, err := h.Handle(context.Background(), RedeemJoinTokenCommand{AccountID: uuid.New(), Token: "revoked-code"})
	if !errors.Is(err, ErrJoinTokenRevoked) {
		t.Fatalf("error = %v, want ErrJoinTokenRevoked", err)
	}

	if prov.created != nil {
		t.Error("a revoked code must not provision a member")
	}
}

func TestRedeemJoinTokenRejectsUnknown(t *testing.T) {
	t.Parallel()

	tokens := fakeJoinTokenReader{err: adaptererr.ErrNotFound}
	prov := &fakePendingProvisioner{}

	h := MustNewRedeemJoinTokenHandler(tokens, prov, fakeMemberByAccount{}, fakeTransactor{}, nil)

	_, err := h.Handle(context.Background(), RedeemJoinTokenCommand{AccountID: uuid.New(), Token: "nope"})
	if !errors.Is(err, ErrJoinTokenInvalid) {
		t.Fatalf("error = %v, want ErrJoinTokenInvalid", err)
	}

	if prov.created != nil {
		t.Error("an unknown code must not provision a member")
	}
}

func TestRedeemJoinTokenIdempotentExistingMember(t *testing.T) {
	t.Parallel()

	projID := uuid.New()
	existing := model.Member{ID: uuid.New(), Status: model.MemberStatusActive, AccountID: uuid.New()}

	tokens := fakeJoinTokenReader{token: repository.JoinToken{OrgID: uuid.New(), ProjectID: projID}}
	prov := &fakePendingProvisioner{}
	byAccount := fakeMemberByAccount{member: existing, found: true}

	h := MustNewRedeemJoinTokenHandler(tokens, prov, byAccount, fakeTransactor{}, nil)

	res, err := h.Handle(context.Background(), RedeemJoinTokenCommand{AccountID: uuid.New(), Token: "code"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if res.MemberID != existing.ID {
		t.Errorf("MemberID = %s, want the existing member %s", res.MemberID, existing.ID)
	}

	if !res.AlreadyMember {
		t.Error("AlreadyMember = false, want true for an idempotent redemption")
	}

	if res.ProjectID != projID {
		t.Errorf("ProjectID = %s, want the token's project %s", res.ProjectID, projID)
	}

	// Never duplicate, never downgrade an existing active member to pending.
	if prov.created != nil {
		t.Error("an existing member must not be re-created or downgraded")
	}
}

// TestRedeemJoinTokenProvisionsFreshForOffboardedMember proves the re-join fix:
// an account whose only existing member was offboarded (removed, and separately
// purged/deactivated/suspended) is NOT treated as an idempotent already-member.
// It falls through and provisions a brand-new pending member so the person can
// start over.
func TestRedeemJoinTokenProvisionsFreshForOffboardedMember(t *testing.T) {
	t.Parallel()

	deadStatuses := []model.MemberStatus{
		model.MemberStatusRemoved,
		model.MemberStatusPurged,
		model.MemberStatusDeactivated,
		model.MemberStatusSuspended,
	}

	for _, dead := range deadStatuses {
		t.Run(string(dead), func(t *testing.T) {
			t.Parallel()

			projID := uuid.New()
			accountID := uuid.New()
			ghost := model.Member{ID: uuid.New(), Status: dead, AccountID: accountID}

			tokens := fakeJoinTokenReader{token: repository.JoinToken{OrgID: uuid.New(), ProjectID: projID}}
			prov := &fakePendingProvisioner{}
			byAccount := fakeMemberByAccount{member: ghost, found: true}

			h := MustNewRedeemJoinTokenHandler(tokens, prov, byAccount, fakeTransactor{}, nil)

			res, err := h.Handle(context.Background(), RedeemJoinTokenCommand{
				AccountID:   accountID,
				Token:       "code",
				Email:       "rejoiner@example.com",
				DisplayName: "Rejoiner",
			})
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}

			if res.AlreadyMember {
				t.Error("AlreadyMember = true, want false: a dead member is not idempotency")
			}

			if prov.created == nil {
				t.Fatal("expected a fresh member to be provisioned for the offboarded account")
			}

			if prov.created.Status != model.MemberStatusPending {
				t.Errorf("fresh member status = %q, want pending", prov.created.Status)
			}

			if prov.created.ID == ghost.ID {
				t.Error("fresh member reused the offboarded member's id; want a brand-new member")
			}

			if res.MemberID != prov.created.ID {
				t.Errorf("result MemberID = %s, want the freshly created member %s", res.MemberID, prov.created.ID)
			}

			if prov.created.AccountID != accountID {
				t.Errorf("fresh member account_id = %s, want %s", prov.created.AccountID, accountID)
			}
		})
	}
}

// TestRedeemJoinTokenCrossOrgProvisionsFresh proves the org-scoped idempotency
// fix: an account that is a LIVE member of org A redeeming a join code for org B
// is NOT treated as already-a-member — the guard is scoped to the CODE'S org, so
// the account gets a fresh pending member in org B.
func TestRedeemJoinTokenCrossOrgProvisionsFresh(t *testing.T) {
	t.Parallel()

	orgA := uuid.New()
	orgB := uuid.New()
	projInB := uuid.New()
	accountID := uuid.New()

	// The account is a LIVE member of org A only.
	liveInA := model.Member{ID: uuid.New(), Status: model.MemberStatusActive, AccountID: accountID}

	tokens := fakeJoinTokenReader{token: repository.JoinToken{OrgID: orgB, ProjectID: projInB}}
	prov := &fakePendingProvisioner{}
	byAccount := fakeMemberByAccount{member: liveInA, inOrg: orgA, found: true}

	h := MustNewRedeemJoinTokenHandler(tokens, prov, byAccount, fakeTransactor{}, nil)

	res, err := h.Handle(context.Background(), RedeemJoinTokenCommand{
		AccountID:   accountID,
		Token:       "org-b-code",
		Email:       "cross@example.com",
		DisplayName: "Cross Org",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if res.AlreadyMember {
		t.Error("AlreadyMember = true, want false: a live member of org A must still join org B")
	}

	if prov.created == nil {
		t.Fatal("expected a fresh member to be provisioned in org B")
	}

	if prov.created.Status != model.MemberStatusPending {
		t.Errorf("fresh member status = %q, want pending", prov.created.Status)
	}

	if prov.created.ID == liveInA.ID {
		t.Error("fresh member reused the org-A member's id; want a brand-new member")
	}

	if res.ProjectID != projInB {
		t.Errorf("ProjectID = %s, want the code's project %s", res.ProjectID, projInB)
	}
}

func TestRedeemJoinTokenRespectsSeatGate(t *testing.T) {
	t.Parallel()

	tokens := fakeJoinTokenReader{token: repository.JoinToken{OrgID: uuid.New(), ProjectID: uuid.New()}}
	prov := &fakePendingProvisioner{}
	gate := &fakeSeatGate{err: errs.InvalidError{Field: "seats", Reason: "seat limit reached"}}

	h := MustNewRedeemJoinTokenHandler(tokens, prov, fakeMemberByAccount{}, fakeTransactor{}, gate)

	_, err := h.Handle(context.Background(), RedeemJoinTokenCommand{AccountID: uuid.New(), Token: "code"})
	if err == nil {
		t.Fatal("expected the seat gate to block the join")
	}

	if !gate.called {
		t.Error("seat gate was not consulted")
	}

	if prov.created != nil {
		t.Error("a seat-capped join must not provision a member")
	}
}
