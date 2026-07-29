// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

func TestCreateOrganizationHandler(t *testing.T) {
	t.Parallel()

	validAccountID := uuid.New()

	cases := []struct {
		name      string
		cmd       CreateOrganizationCommand
		createErr error
		wantErr   bool
		// wantInvalid is true when the error must be an errs.InvalidError.
		wantInvalid bool
		// wantDuplicate is true when the error must be an errs.DuplicateError.
		wantDuplicate bool
	}{
		{
			name: "happy_path",
			cmd:  CreateOrganizationCommand{Name: "Acme Corp", CreatorAccountID: validAccountID},
		},
		{
			name:        "empty_name_rejected",
			cmd:         CreateOrganizationCommand{Name: "", CreatorAccountID: validAccountID},
			wantErr:     true,
			wantInvalid: true,
		},
		{
			name:        "blank_name_rejected",
			cmd:         CreateOrganizationCommand{Name: "   ", CreatorAccountID: validAccountID},
			wantErr:     true,
			wantInvalid: true,
		},
		{
			name:        "nil_creator_account_id_rejected",
			cmd:         CreateOrganizationCommand{Name: "Acme", CreatorAccountID: uuid.Nil},
			wantErr:     true,
			wantInvalid: true,
		},
		{
			name:      "store_error_propagated",
			cmd:       CreateOrganizationCommand{Name: "Acme", CreatorAccountID: validAccountID},
			createErr: errors.New("connection lost"),
			wantErr:   true,
		},
		{
			name:          "store_duplicate_translated",
			cmd:           CreateOrganizationCommand{Name: "Acme", CreatorAccountID: validAccountID},
			createErr:     adaptererr.ErrDuplicate,
			wantErr:       true,
			wantDuplicate: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			capture := &orgProvisionCapture{createErr: tc.createErr}
			hook := &fakeOrgCreatedHook{}
			accounts := &fakeCreatorAccountReader{account: model.Account{
				ID:    tc.cmd.CreatorAccountID,
				Email: "owner@acme.test",
			}, err: nil}
			h := MustNewCreateOrganizationHandler(fakeOrgWriter{capture}, fakeGlobalProjectWriter{capture}, fakeCreatorMemberWriter{capture}, fakeTransactor{}, accounts, hook, nil)

			result, err := h.Handle(t.Context(), tc.cmd)

			if tc.wantErr {
				assertWantedCreateOrgError(t, err, tc.wantInvalid, tc.wantDuplicate)

				return
			}

			if err != nil {
				t.Fatalf("Handle: unexpected error: %v", err)
			}

			// Happy path: both IDs must be non-nil.
			if result.OrgID == uuid.Nil {
				t.Error("Handle: OrgID must not be nil")
			}

			if result.GlobalProjectID == uuid.Nil {
				t.Error("Handle: GlobalProjectID must not be nil")
			}

			// The store must have been called with consistent data.
			if capture.org == nil {
				t.Fatal("Handle: organizationCreator not called")
			}

			if capture.org.ID != result.OrgID {
				t.Errorf("org.ID = %v, want %v", capture.org.ID, result.OrgID)
			}

			if capture.project.ID != result.GlobalProjectID {
				t.Errorf("project.ID = %v, want %v", capture.project.ID, result.GlobalProjectID)
			}

			if capture.project.OrgID != result.OrgID {
				t.Errorf("project.OrgID = %v, want %v (org ID)", capture.project.OrgID, result.OrgID)
			}

			// The org-created hook (SaaS billing trial in production) fires inside
			// the creation transaction with the new org id.
			if !hook.called || hook.orgID != result.OrgID {
				t.Errorf("onCreated hook: called=%t orgID=%v, want fired for %v", hook.called, hook.orgID, result.OrgID)
			}

			if capture.project.Name != "default" {
				t.Errorf("project.Name = %q, want %q", capture.project.Name, "default")
			}

			if capture.member.AccountID != tc.cmd.CreatorAccountID {
				t.Errorf("member.AccountID = %v, want %v", capture.member.AccountID, tc.cmd.CreatorAccountID)
			}
		})
	}
}

// assertWantedCreateOrgError checks the expected error shape for the failing cases.
func assertWantedCreateOrgError(t *testing.T, err error, wantInvalid, wantDuplicate bool) {
	t.Helper()

	if err == nil {
		t.Fatal("Handle: want error, got nil")
	}

	if wantInvalid {
		var inv errs.InvalidError
		if !errors.As(err, &inv) {
			t.Errorf("Handle: want errs.InvalidError, got %T: %v", err, err)
		}
	}

	if wantDuplicate {
		var dup errs.DuplicateError
		if !errors.As(err, &dup) {
			t.Errorf("Handle: want errs.DuplicateError, got %T: %v", err, err)
		}
	}
}
