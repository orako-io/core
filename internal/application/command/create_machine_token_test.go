// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// fakeMachineTokenMinter is a machineTokenMinter that records the last mint
// call and returns a fixed result.
type fakeMachineTokenMinter struct {
	result        MintedMachineToken
	err           error `exhaustruct:"optional"`
	calls         int
	gotMemberID   uuid.UUID
	gotProjectIDs []uuid.UUID
	gotLabel      string
}

func (f *fakeMachineTokenMinter) MintMachineToken(_ context.Context, memberID uuid.UUID, projectIDs []uuid.UUID, label string) (MintedMachineToken, error) {
	f.calls++
	f.gotMemberID = memberID
	f.gotProjectIDs = projectIDs
	f.gotLabel = label

	return f.result, f.err
}

// fakeMachineTokenProjects is a machineTokenProjectReader backed by a fixed
// map of project id → project.
type fakeMachineTokenProjects struct {
	projects map[uuid.UUID]model.Project
}

func (f *fakeMachineTokenProjects) ByID(_ context.Context, id uuid.UUID) (model.Project, error) {
	p, ok := f.projects[id]
	if !ok {
		return model.Project{}, adaptererr.ErrNotFound
	}

	return p, nil
}

func TestCreateMachineToken_AdminMints(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	memberID := uuid.New()
	projID := uuid.New()
	tokenID := uuid.New()
	now := time.Now()

	minter := &fakeMachineTokenMinter{result: MintedMachineToken{
		ID:        tokenID,
		Secret:    "mcp_at_test-secret",
		CreatedAt: now,
		ExpiresAt: now.Add(365 * 24 * time.Hour),
	}}
	projects := &fakeMachineTokenProjects{projects: map[uuid.UUID]model.Project{
		projID: {ID: projID, OrgID: orgID},
	}}

	h := MustNewCreateMachineTokenHandler(minter, projects)

	result, err := h.Handle(t.Context(), CreateMachineTokenCommand{
		MemberID:   memberID,
		OrgID:      orgID,
		IsOrgAdmin: true,
		Label:      "  CI agent  ",
		ProjectIDs: []uuid.UUID{projID},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if result.ID != tokenID || result.Secret != "mcp_at_test-secret" {
		t.Errorf("result = %+v, want id=%s secret=mcp_at_test-secret", result, tokenID)
	}

	if result.Label != "CI agent" {
		t.Errorf("Label = %q, want trimmed \"CI agent\"", result.Label)
	}

	if minter.gotMemberID != memberID {
		t.Errorf("mint memberID = %s, want the caller %s", minter.gotMemberID, memberID)
	}

	if len(minter.gotProjectIDs) != 1 || minter.gotProjectIDs[0] != projID {
		t.Errorf("mint projectIDs = %v, want [%s]", minter.gotProjectIDs, projID)
	}
}

// TestCreateMachineToken_UnscopedIsANoOpCheck proves an empty ProjectIDs
// (unscoped) skips the org-membership check entirely — it means "every
// project the member can reach", not "every project in the org", so there is
// nothing to verify against orgID.
func TestCreateMachineToken_UnscopedIsANoOpCheck(t *testing.T) {
	t.Parallel()

	minter := &fakeMachineTokenMinter{result: MintedMachineToken{ID: uuid.New(), Secret: "mcp_at_x"}}
	projects := &fakeMachineTokenProjects{projects: map[uuid.UUID]model.Project{}}

	h := MustNewCreateMachineTokenHandler(minter, projects)

	_, err := h.Handle(t.Context(), CreateMachineTokenCommand{
		MemberID:   uuid.New(),
		OrgID:      uuid.New(),
		IsOrgAdmin: true,
		Label:      "unscoped",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if minter.calls != 1 {
		t.Errorf("mint calls = %d, want 1", minter.calls)
	}
}

func TestCreateMachineToken_Rejections(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	otherOrgID := uuid.New()
	projInOtherOrg := uuid.New()

	cases := []struct {
		name          string
		cmd           CreateMachineTokenCommand
		wantForbidden bool
		wantInvalid   bool
	}{
		{
			name:          "non-admin forbidden",
			cmd:           CreateMachineTokenCommand{OrgID: orgID, IsOrgAdmin: false, Label: "x"},
			wantForbidden: true,
		},
		{
			name:        "no org resolved",
			cmd:         CreateMachineTokenCommand{OrgID: uuid.Nil, IsOrgAdmin: true, Label: "x"},
			wantInvalid: true,
		},
		{
			name:        "blank label",
			cmd:         CreateMachineTokenCommand{OrgID: orgID, IsOrgAdmin: true, Label: "   "},
			wantInvalid: true,
		},
		{
			name: "project scoped to a different org is forbidden",
			cmd: CreateMachineTokenCommand{
				OrgID: orgID, IsOrgAdmin: true, Label: "x",
				ProjectIDs: []uuid.UUID{projInOtherOrg},
			},
			wantForbidden: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			minter := &fakeMachineTokenMinter{result: MintedMachineToken{ID: uuid.New(), Secret: "mcp_at_x"}}
			projects := &fakeMachineTokenProjects{projects: map[uuid.UUID]model.Project{
				projInOtherOrg: {ID: projInOtherOrg, OrgID: otherOrgID},
			}}

			_, err := MustNewCreateMachineTokenHandler(minter, projects).Handle(t.Context(), tc.cmd)
			if err == nil {
				t.Fatal("Handle: want an error, got nil")
			}

			if tc.wantForbidden {
				var forbidden errs.ForbiddenError
				if !errors.As(err, &forbidden) {
					t.Fatalf("want ForbiddenError, got %v", err)
				}
			}

			if tc.wantInvalid {
				var invalid errs.InvalidError
				if !errors.As(err, &invalid) {
					t.Fatalf("want InvalidError, got %v", err)
				}
			}

			if minter.calls != 0 {
				t.Error("a rejected command must not mint a token")
			}
		})
	}
}
