// SPDX-License-Identifier: AGPL-3.0-or-later

package conversation_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/conversation"
	"github.com/orako-io/core/internal/adapters/integration"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// int64Ptr is a test-only convenience for UpdateSettings' presence-based
// alertSeconds parameter (nil = unchanged, non-nil = overwrite).
func int64Ptr(v int64) *int64 {
	return &v
}

// TestEscalationStore_Settings_DefaultsToNil proves a fresh organization has
// no explicit escalation choices — every field nil/empty, defaults apply.
func TestEscalationStore_Settings_DefaultsToNil(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := conversation.NewEscalationStore(pool)
	orgID := testsupport.SeedOrganization(t, pool)

	got, err := store.ReadSettings(t.Context(), orgID)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}

	if !got.ClaimIsDefault || !got.AlertIsDefault {
		t.Errorf("fresh org must have nil escalation settings, got %+v", got)
	}

	if got.DefaultAlertChannelID != "" {
		t.Errorf("DefaultAlertChannelID = %q, want empty", got.DefaultAlertChannelID)
	}
}

// TestEscalationStore_UpdateSettings_AlertFieldsRoundTrip proves
// alert_timeout_seconds and default_alert_channel_id persist through
// UpdateSettings/Settings, and that the "-" sentinel clears the channel while
// an empty string leaves it unchanged.
func TestEscalationStore_UpdateSettings_AlertFieldsRoundTrip(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := conversation.NewEscalationStore(pool)
	orgID := testsupport.SeedOrganization(t, pool)

	alertSeconds := int64(14400)
	if err := store.UpdateSettings(t.Context(), orgID, 600, &alertSeconds, "C0DEFAULT"); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	got, err := store.ReadSettings(t.Context(), orgID)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}

	if got.ClaimIsDefault || got.ClaimTimeoutSeconds != 600 {
		t.Errorf("ClaimTimeoutSeconds = %v, want 600", got.ClaimTimeoutSeconds)
	}

	if got.AlertIsDefault || got.AlertTimeoutSeconds != 14400 {
		t.Errorf("AlertTimeoutSeconds = %v, want 14400", got.AlertTimeoutSeconds)
	}

	if got.DefaultAlertChannelID != "C0DEFAULT" {
		t.Errorf("DefaultAlertChannelID = %q, want C0DEFAULT", got.DefaultAlertChannelID)
	}

	// Empty string leaves the channel unchanged; an explicit 0 disables the
	// alert rung (present, not absent).
	zeroAlert := int64(0)
	if err := store.UpdateSettings(t.Context(), orgID, 600, &zeroAlert, ""); err != nil {
		t.Fatalf("UpdateSettings (unchanged): %v", err)
	}

	unchanged, err := store.ReadSettings(t.Context(), orgID)
	if err != nil {
		t.Fatalf("Settings (unchanged): %v", err)
	}

	if unchanged.DefaultAlertChannelID != "C0DEFAULT" {
		t.Errorf("DefaultAlertChannelID after empty-string update = %q, want unchanged C0DEFAULT", unchanged.DefaultAlertChannelID)
	}

	if unchanged.AlertIsDefault || unchanged.AlertTimeoutSeconds != 0 {
		t.Errorf("AlertTimeoutSeconds after disabling = %v, want 0", unchanged.AlertTimeoutSeconds)
	}

	// The "-" sentinel clears the channel.
	if err := store.UpdateSettings(t.Context(), orgID, 600, &zeroAlert, "-"); err != nil {
		t.Fatalf("UpdateSettings (clear): %v", err)
	}

	cleared, err := store.ReadSettings(t.Context(), orgID)
	if err != nil {
		t.Fatalf("Settings (clear): %v", err)
	}

	if cleared.DefaultAlertChannelID != "" {
		t.Errorf("DefaultAlertChannelID after clear = %q, want empty", cleared.DefaultAlertChannelID)
	}
}

// TestEscalationStore_UpdateSettings_AlertTimeoutAbsentPreservesStoredValue
// proves the review #15 fix: a nil alertSeconds (the wire's absent optional
// field) leaves the stored alert_timeout_seconds untouched — an older client
// that never sets the field must not silently disable alerts — while a
// present 0 explicitly disables it.
func TestEscalationStore_UpdateSettings_AlertTimeoutAbsentPreservesStoredValue(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := conversation.NewEscalationStore(pool)
	orgID := testsupport.SeedOrganization(t, pool)

	seeded := int64(7200)
	if err := store.UpdateSettings(t.Context(), orgID, 600, &seeded, ""); err != nil {
		t.Fatalf("UpdateSettings (seed): %v", err)
	}

	// Absent (nil) must leave the stored value untouched.
	if err := store.UpdateSettings(t.Context(), orgID, 600, nil, ""); err != nil {
		t.Fatalf("UpdateSettings (absent): %v", err)
	}

	got, err := store.ReadSettings(t.Context(), orgID)
	if err != nil {
		t.Fatalf("Settings (absent): %v", err)
	}

	if got.AlertIsDefault || got.AlertTimeoutSeconds != 7200 {
		t.Errorf("AlertTimeoutSeconds after absent update = %v, want unchanged 7200", got.AlertTimeoutSeconds)
	}

	// A present 0 explicitly disables the rung.
	zero := int64(0)
	if err := store.UpdateSettings(t.Context(), orgID, 600, &zero, ""); err != nil {
		t.Fatalf("UpdateSettings (disable): %v", err)
	}

	disabled, err := store.ReadSettings(t.Context(), orgID)
	if err != nil {
		t.Fatalf("Settings (disable): %v", err)
	}

	if disabled.AlertIsDefault || disabled.AlertTimeoutSeconds != 0 {
		t.Errorf("AlertTimeoutSeconds after explicit-0 update = %v, want 0", disabled.AlertTimeoutSeconds)
	}
}

// ---- alert-channel rung (task 4, phase 3) ----------------------------------

// findAlertCandidate returns the row for id, so a test can assert on it
// without assuming the store returns only what this test seeded — other
// parallel/prior test runs leave their own rows in the same table.
func findAlertCandidate(got []model.AlertCandidate, id uuid.UUID) (model.AlertCandidate, bool) {
	for _, row := range got {
		if row.ConversationID == id {
			return row, true
		}
	}

	return model.AlertCandidate{}, false
}

// TestEscalationStore_UnclaimedForAlert_PastCutoffResolvesChannelsAndCount
// proves a conversation past its org's alert timeout comes back with the
// project's own alert channel, the org fallback, and the active-only
// candidate count (the excluded candidate does not count).
func TestEscalationStore_UnclaimedForAlert_PastCutoffResolvesChannelsAndCount(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := conversation.NewEscalationStore(pool)

	orgID := testsupport.SeedOrganization(t, pool)
	if err := store.UpdateSettings(t.Context(), orgID, 0, int64Ptr(60), "ORGCHAN"); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	projectID := testsupport.SeedProjectInOrg(t, pool, orgID)

	if err := integration.NewProjectProviderStore(pool, nil).UpsertProvider(t.Context(), projectID, "slack", []byte(`{}`), []string{"PROJCHAN"}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}

	convID := testsupport.SeedAlertCandidate(t, pool, projectID, 2*time.Minute)

	got, err := store.UnclaimedForAlert(t.Context(), int64(model.DefaultAlertTimeout/time.Second))
	if err != nil {
		t.Fatalf("UnclaimedForAlert: %v", err)
	}

	row, found := findAlertCandidate(got, convID)
	if !found {
		t.Fatalf("UnclaimedForAlert did not return convID %s", convID)
	}

	if len(row.ProjectAlertChannelIDs) != 1 || row.ProjectAlertChannelIDs[0] != "PROJCHAN" {
		t.Errorf("ProjectAlertChannelIDs = %v, want [PROJCHAN]", row.ProjectAlertChannelIDs)
	}

	if row.OrgAlertChannelID != "ORGCHAN" {
		t.Errorf("OrgAlertChannelID = %q, want ORGCHAN", row.OrgAlertChannelID)
	}

	if row.CandidateCount != 1 {
		t.Errorf("CandidateCount = %d, want 1 (excluded candidate must not count)", row.CandidateCount)
	}
}

// TestEscalationStore_UnclaimedForAlert_BoundaryNotYetElapsed proves a
// conversation one second under the cutoff is not returned.
func TestEscalationStore_UnclaimedForAlert_BoundaryNotYetElapsed(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := conversation.NewEscalationStore(pool)

	orgID := testsupport.SeedOrganization(t, pool)
	if err := store.UpdateSettings(t.Context(), orgID, 0, int64Ptr(60), ""); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	projectID := testsupport.SeedProjectInOrg(t, pool, orgID)
	convID := testsupport.SeedAlertCandidate(t, pool, projectID, 59*time.Second)

	got, err := store.UnclaimedForAlert(t.Context(), int64(model.DefaultAlertTimeout/time.Second))
	if err != nil {
		t.Fatalf("UnclaimedForAlert: %v", err)
	}

	if _, found := findAlertCandidate(got, convID); found {
		t.Error("UnclaimedForAlert at cutoff-1s must not return this conversation")
	}
}

// TestEscalationStore_UnclaimedForAlert_DisabledWhenZero proves a stored 0
// disables the rule even for a very old conversation.
func TestEscalationStore_UnclaimedForAlert_DisabledWhenZero(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := conversation.NewEscalationStore(pool)

	orgID := testsupport.SeedOrganization(t, pool)
	if err := store.UpdateSettings(t.Context(), orgID, 0, int64Ptr(0), ""); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	projectID := testsupport.SeedProjectInOrg(t, pool, orgID)
	convID := testsupport.SeedAlertCandidate(t, pool, projectID, 24*time.Hour)

	got, err := store.UnclaimedForAlert(t.Context(), int64(model.DefaultAlertTimeout/time.Second))
	if err != nil {
		t.Fatalf("UnclaimedForAlert: %v", err)
	}

	if _, found := findAlertCandidate(got, convID); found {
		t.Error("UnclaimedForAlert with alert_timeout_seconds=0 must not return this conversation (disabled)")
	}
}

// TestEscalationStore_UnclaimedForAlert_PrefersProviderWithAlertChannelSet
// proves the review #13 fix: when a project has two providers and only one
// has an alert_channel_id configured, the resolved project_alert_channel_id
// is that one regardless of which provider was most recently updated (e.g.
// re-saving Telegram after Slack must not shadow Slack's configured alert
// channel).
func TestEscalationStore_UnclaimedForAlert_PrefersProviderWithAlertChannelSet(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := conversation.NewEscalationStore(pool)

	orgID := testsupport.SeedOrganization(t, pool)
	if err := store.UpdateSettings(t.Context(), orgID, 0, int64Ptr(60), ""); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	projectID := testsupport.SeedProjectInOrg(t, pool, orgID)

	providerStore := integration.NewProjectProviderStore(pool, nil)
	if err := providerStore.UpsertProvider(t.Context(), projectID, "slack", []byte(`{}`), []string{"SLACKCHAN"}); err != nil {
		t.Fatalf("UpsertProvider(slack): %v", err)
	}

	// Telegram is configured (and re-saved) after Slack, so updated_at DESC
	// alone would pick it — but it carries no alert channel.
	if err := providerStore.UpsertProvider(t.Context(), projectID, "telegram", []byte(`{}`), nil); err != nil {
		t.Fatalf("UpsertProvider(telegram): %v", err)
	}

	convID := testsupport.SeedAlertCandidate(t, pool, projectID, 2*time.Minute)

	got, err := store.UnclaimedForAlert(t.Context(), int64(model.DefaultAlertTimeout/time.Second))
	if err != nil {
		t.Fatalf("UnclaimedForAlert: %v", err)
	}

	row, found := findAlertCandidate(got, convID)
	if !found {
		t.Fatalf("UnclaimedForAlert did not return convID %s", convID)
	}

	if len(row.ProjectAlertChannelIDs) != 1 || row.ProjectAlertChannelIDs[0] != "SLACKCHAN" {
		t.Errorf("ProjectAlertChannelIDs = %v, want [SLACKCHAN] (must not be shadowed by the alert-channel-less, more-recently-updated telegram row)", row.ProjectAlertChannelIDs)
	}
}

// TestEscalationStore_MarkAlerted_CASFiresOnceThenExcludesTheRow proves the
// once-only marker: the first MarkAlerted wins and the row drops out of
// UnclaimedForAlert; a second MarkAlerted on the same conversation loses.
func TestEscalationStore_MarkAlerted_CASFiresOnceThenExcludesTheRow(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := conversation.NewEscalationStore(pool)

	orgID := testsupport.SeedOrganization(t, pool)
	if err := store.UpdateSettings(t.Context(), orgID, 0, int64Ptr(60), ""); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	projectID := testsupport.SeedProjectInOrg(t, pool, orgID)
	convID := testsupport.SeedAlertCandidate(t, pool, projectID, 2*time.Minute)

	won, err := store.MarkAlerted(t.Context(), convID)
	if err != nil {
		t.Fatalf("MarkAlerted: %v", err)
	}

	if !won {
		t.Fatal("first MarkAlerted must win")
	}

	again, err := store.MarkAlerted(t.Context(), convID)
	if err != nil {
		t.Fatalf("MarkAlerted (second): %v", err)
	}

	if again {
		t.Error("second MarkAlerted must lose (already alerted)")
	}

	got, err := store.UnclaimedForAlert(t.Context(), int64(model.DefaultAlertTimeout/time.Second))
	if err != nil {
		t.Fatalf("UnclaimedForAlert: %v", err)
	}

	if _, found := findAlertCandidate(got, convID); found {
		t.Error("UnclaimedForAlert after MarkAlerted must not return this conversation")
	}
}
