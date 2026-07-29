// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

type fakeOrgSettingsReader struct {
	view OrgSettingsView
}

func (f *fakeOrgSettingsReader) ReadSettings(_ context.Context, _ uuid.UUID) (OrgSettingsView, error) {
	return f.view, nil
}

// TestGetOrganizationSettings_ReturnsReaderView proves the admin path returns the
// reader's view verbatim (the effective-value resolution now lives in the
// adapter / domain, tested there).
func TestGetOrganizationSettings_ReturnsReaderView(t *testing.T) {
	t.Parallel()

	want := OrgSettingsView{ClaimTimeoutSeconds: 600, AlertTimeoutSeconds: 0, DefaultAlertChannelID: "C0DEFAULT"}
	h := MustNewGetOrganizationSettingsHandler(&fakeOrgSettingsReader{view: want})

	got, err := h.Handle(t.Context(), GetOrganizationSettingsQuery{OrgID: uuid.New(), IsOrgAdmin: true})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if got != want {
		t.Errorf("view = %+v, want %+v", got, want)
	}
}

func TestGetOrganizationSettings_NonAdminForbidden(t *testing.T) {
	t.Parallel()

	h := MustNewGetOrganizationSettingsHandler(&fakeOrgSettingsReader{})

	_, err := h.Handle(t.Context(), GetOrganizationSettingsQuery{OrgID: uuid.New(), IsOrgAdmin: false})

	var forbidden errs.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("want ForbiddenError, got %v", err)
	}
}
