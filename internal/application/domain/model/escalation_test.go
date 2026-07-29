// SPDX-License-Identifier: AGPL-3.0-or-later

package model_test

import (
	"testing"
	"time"

	"github.com/orako-io/core/internal/application/domain/model"
)

// TestOrgEscalationSettings_Defaults proves that unset (nil) knobs resolve to the
// product defaults.
func TestOrgEscalationSettings_Defaults(t *testing.T) {
	t.Parallel()

	var s model.OrgEscalationSettings

	if got, want := s.EffectiveClaimTimeoutSeconds(), int64(model.DefaultClaimTimeout/time.Second); got != want {
		t.Errorf("EffectiveClaimTimeoutSeconds = %d, want default %d", got, want)
	}

	if got, want := s.EffectiveAlertTimeoutSeconds(), int64(model.DefaultAlertTimeout/time.Second); got != want {
		t.Errorf("EffectiveAlertTimeoutSeconds = %d, want default %d", got, want)
	}
}

// TestOrgEscalationSettings_StoredValuesWin proves explicit values (zero
// included, which disables the alert) override the defaults.
func TestOrgEscalationSettings_StoredValuesWin(t *testing.T) {
	t.Parallel()

	claim, alert := int64(600), int64(0)
	s := model.OrgEscalationSettings{
		ClaimTimeoutSeconds:   &claim,
		AlertTimeoutSeconds:   &alert,
		DefaultAlertChannelID: "C0DEFAULT",
	}

	if got := s.EffectiveClaimTimeoutSeconds(); got != 600 {
		t.Errorf("EffectiveClaimTimeoutSeconds = %d, want explicit 600", got)
	}

	if got := s.EffectiveAlertTimeoutSeconds(); got != 0 {
		t.Errorf("EffectiveAlertTimeoutSeconds = %d, want explicit 0 (disabled)", got)
	}
}
