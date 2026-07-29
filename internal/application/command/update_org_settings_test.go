// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

type fakeOrgSettingsWriter struct {
	orgID                 uuid.UUID
	claim                 int64
	alert                 *int64
	defaultAlertChannelID string
	calls                 int
}

func (f *fakeOrgSettingsWriter) UpdateSettings(_ context.Context, orgID uuid.UUID, claimSeconds int64, alertSeconds *int64, defaultAlertChannelID string) error {
	f.orgID, f.claim, f.alert, f.defaultAlertChannelID = orgID, claimSeconds, alertSeconds, defaultAlertChannelID
	f.calls++

	return nil
}

func TestUpdateOrganizationSettings_AdminWrites(t *testing.T) {
	t.Parallel()

	writer := &fakeOrgSettingsWriter{}
	h := MustNewUpdateOrganizationSettingsHandler(writer)
	orgID := uuid.New()

	err := h.Handle(t.Context(), UpdateOrganizationSettingsCommand{
		OrgID:               orgID,
		IsOrgAdmin:          true,
		ClaimTimeoutSeconds: 600,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if writer.orgID != orgID || writer.claim != 600 {
		t.Errorf("stored (%s, %d), want (%s, 600)", writer.orgID, writer.claim, orgID)
	}
}

// TestUpdateOrganizationSettings_AlertFieldsPassThrough proves the
// alert-timeout and default-alert-channel fields reach the store unchanged.
func TestUpdateOrganizationSettings_AlertFieldsPassThrough(t *testing.T) {
	t.Parallel()

	writer := &fakeOrgSettingsWriter{}
	h := MustNewUpdateOrganizationSettingsHandler(writer)
	orgID := uuid.New()

	alertSeconds := int64(14400)

	err := h.Handle(t.Context(), UpdateOrganizationSettingsCommand{
		OrgID:                 orgID,
		IsOrgAdmin:            true,
		ClaimTimeoutSeconds:   600,
		AlertTimeoutSeconds:   &alertSeconds,
		DefaultAlertChannelID: "C0DEFAULT",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if writer.alert == nil || *writer.alert != 14400 {
		t.Errorf("alert = %v, want 14400", writer.alert)
	}

	if writer.defaultAlertChannelID != "C0DEFAULT" {
		t.Errorf("defaultAlertChannelID = %q, want C0DEFAULT", writer.defaultAlertChannelID)
	}
}

// TestUpdateOrganizationSettings_AlertTimeoutAbsent_PassesNilThrough proves
// an absent alert_timeout_seconds (nil) reaches the store as nil — presence
// distinguishes "unset" from an explicit 0, so the store leaves the stored
// value unchanged instead of disabling alerts.
func TestUpdateOrganizationSettings_AlertTimeoutAbsent_PassesNilThrough(t *testing.T) {
	t.Parallel()

	writer := &fakeOrgSettingsWriter{}
	h := MustNewUpdateOrganizationSettingsHandler(writer)
	orgID := uuid.New()

	err := h.Handle(t.Context(), UpdateOrganizationSettingsCommand{
		OrgID:               orgID,
		IsOrgAdmin:          true,
		ClaimTimeoutSeconds: 600,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if writer.alert != nil {
		t.Errorf("alert = %v, want nil (absent field must leave the stored value unchanged)", writer.alert)
	}
}

// TestUpdateOrganizationSettings_AlertTimeoutZero_PassesZeroThrough proves an
// explicit 0 reaches the store as a non-nil pointer to 0 — distinct from the
// absent case, and disables the alert rung.
func TestUpdateOrganizationSettings_AlertTimeoutZero_PassesZeroThrough(t *testing.T) {
	t.Parallel()

	writer := &fakeOrgSettingsWriter{}
	h := MustNewUpdateOrganizationSettingsHandler(writer)
	orgID := uuid.New()

	zero := int64(0)

	err := h.Handle(t.Context(), UpdateOrganizationSettingsCommand{
		OrgID:               orgID,
		IsOrgAdmin:          true,
		ClaimTimeoutSeconds: 600,
		AlertTimeoutSeconds: &zero,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if writer.alert == nil || *writer.alert != 0 {
		t.Errorf("alert = %v, want pointer to 0", writer.alert)
	}
}

func TestUpdateOrganizationSettings_Rejections(t *testing.T) {
	t.Parallel()

	negativeAlertTimeout := int64(-1)

	cases := []struct {
		name          string
		cmd           UpdateOrganizationSettingsCommand
		wantForbidden bool
	}{
		{
			name:          "non-admin forbidden",
			cmd:           UpdateOrganizationSettingsCommand{OrgID: uuid.New(), IsOrgAdmin: false, ClaimTimeoutSeconds: 60},
			wantForbidden: true,
		},
		{
			name: "negative claim timeout",
			cmd:  UpdateOrganizationSettingsCommand{OrgID: uuid.New(), IsOrgAdmin: true, ClaimTimeoutSeconds: -1},
		},
		{
			name: "negative alert timeout",
			cmd:  UpdateOrganizationSettingsCommand{OrgID: uuid.New(), IsOrgAdmin: true, ClaimTimeoutSeconds: 60, AlertTimeoutSeconds: &negativeAlertTimeout},
		},
		{
			name: "no org resolved",
			cmd:  UpdateOrganizationSettingsCommand{OrgID: uuid.Nil, IsOrgAdmin: true, ClaimTimeoutSeconds: 60},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			writer := &fakeOrgSettingsWriter{}
			err := MustNewUpdateOrganizationSettingsHandler(writer).Handle(t.Context(), tc.cmd)

			if tc.wantForbidden {
				var forbidden errs.ForbiddenError
				if !errors.As(err, &forbidden) {
					t.Fatalf("want ForbiddenError, got %v", err)
				}
			} else {
				var invalid errs.InvalidError
				if !errors.As(err, &invalid) {
					t.Fatalf("want InvalidError, got %v", err)
				}
			}

			if writer.calls != 0 {
				t.Error("a rejected command must not write")
			}
		})
	}
}
