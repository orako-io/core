// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/identity"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestMemberStoreCreateAndByID proves the create-then-fetch round trip preserves
// all fields including nullable ones (email, display_name, slack_user_id).
func TestMemberStoreCreateAndByID(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewMemberStore(pool)

	cases := []struct {
		name        string
		email       string
		displayName string
		slackUserID string
	}{
		{
			name:        "full_fields",
			email:       "alice@example.com",
			displayName: "Alice",
			slackUserID: "U123",
		},
		{
			name:        "empty_optional",
			email:       "",
			displayName: "No-email Member",
			slackUserID: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			member, err := model.NewMember(uuid.New(), tc.email, tc.displayName)
			if err != nil {
				t.Fatalf("NewMember: %v", err)
			}

			member.SlackUserID = tc.slackUserID

			if err := store.Create(t.Context(), member); err != nil {
				t.Fatalf("Create: %v", err)
			}

			got, err := store.ByID(t.Context(), member.ID)
			if err != nil {
				t.Fatalf("ByID: %v", err)
			}

			if got.ID != member.ID {
				t.Errorf("ID: got %v, want %v", got.ID, member.ID)
			}

			if got.Email != tc.email {
				t.Errorf("Email: got %q, want %q", got.Email, tc.email)
			}

			if got.DisplayName != tc.displayName {
				t.Errorf("DisplayName: got %q, want %q", got.DisplayName, tc.displayName)
			}

			if got.SlackUserID != tc.slackUserID {
				t.Errorf("SlackUserID: got %q, want %q", got.SlackUserID, tc.slackUserID)
			}

			if got.Status != model.MemberStatusInvited {
				t.Errorf("Status: got %q, want %q", got.Status, model.MemberStatusInvited)
			}
		})
	}
}

// TestMemberOnboardingDismissed proves the Get-started dismissal round-trips:
// a fresh member is not dismissed, marking sticks, and undo clears it. A missing
// member is reported as not-found on write.
func TestMemberOnboardingDismissed(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewMemberStore(pool)

	member, _ := model.NewMember(uuid.New(), uuid.NewString()+"@example.test", "Dana")
	if err := store.Create(t.Context(), member); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Fresh member: never dismissed.
	if got, err := store.OnboardingDismissed(t.Context(), member.ID); err != nil || got {
		t.Fatalf("initial OnboardingDismissed = (%v, %v), want (false, nil)", got, err)
	}

	// Dismiss → sticks.
	if err := store.SetOnboardingDismissed(t.Context(), member.ID, true); err != nil {
		t.Fatalf("SetOnboardingDismissed(true): %v", err)
	}

	if got, err := store.OnboardingDismissed(t.Context(), member.ID); err != nil || !got {
		t.Fatalf("after dismiss OnboardingDismissed = (%v, %v), want (true, nil)", got, err)
	}

	// Undo → cleared.
	if err := store.SetOnboardingDismissed(t.Context(), member.ID, false); err != nil {
		t.Fatalf("SetOnboardingDismissed(false): %v", err)
	}

	if got, err := store.OnboardingDismissed(t.Context(), member.ID); err != nil || got {
		t.Fatalf("after undo OnboardingDismissed = (%v, %v), want (false, nil)", got, err)
	}

	// Unknown member on a write → not found.
	if err := store.SetOnboardingDismissed(t.Context(), uuid.New(), true); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("SetOnboardingDismissed(absent) = %v, want ErrNotFound", err)
	}
}

// TestMemberStoreByEmail proves lookup by email works for present and absent emails.
// A UUID-derived email is used so the test is isolated from pre-existing rows.
func TestMemberStoreByEmail(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewMemberStore(pool)

	// Unique per run: prevents collision with rows created by earlier runs.
	id := uuid.New()
	email := id.String() + "@example.test"
	member, _ := model.NewMember(id, email, "Bob")

	if err := store.Create(t.Context(), member); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.ByEmail(t.Context(), email)
	if err != nil {
		t.Fatalf("ByEmail: %v", err)
	}

	if got.ID != member.ID {
		t.Fatalf("ByEmail: got %v, want %v", got.ID, member.ID)
	}

	// Not-found path: use a second unique ID so it cannot match any row.
	absent := uuid.NewString() + "@example.test"
	_, err = store.ByEmail(t.Context(), absent)
	if !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("ByEmail(absent): got %v, want ErrNotFound", err)
	}
}

// TestMemberStoreUpdate proves Update persists mutations and ByID reflects them.
func TestMemberStoreUpdate(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewMemberStore(pool)

	member, _ := model.NewMember(uuid.New(), "carol@example.com", "Carol")

	if err := store.Create(t.Context(), member); err != nil {
		t.Fatalf("Create: %v", err)
	}

	member.DisplayName = "Carol Updated"
	member.SlackUserID = "U999"
	member.Status = model.MemberStatusActive

	if err := store.Update(t.Context(), member); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := store.ByID(t.Context(), member.ID)
	if err != nil {
		t.Fatalf("ByID after update: %v", err)
	}

	if got.DisplayName != "Carol Updated" {
		t.Errorf("DisplayName: got %q, want %q", got.DisplayName, "Carol Updated")
	}

	if got.SlackUserID != "U999" {
		t.Errorf("SlackUserID: got %q, want %q", got.SlackUserID, "U999")
	}

	if got.Status != model.MemberStatusActive {
		t.Errorf("Status: got %q, want %q", got.Status, model.MemberStatusActive)
	}
}

// TestMemberStoreUpdate_BridgeFields proves teams_user_id, discord_user_id,
// and binding_error round-trip through Update/ByID.
func TestMemberStoreUpdate_BridgeFields(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewMemberStore(pool)

	member, _ := model.NewMember(uuid.New(), "dana@example.com", "Dana")

	if err := store.Create(t.Context(), member); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Unique per run (mirrors TestMemberStoreByTeamsUserID /
	// TestMemberStoreByDiscordUserID): teams_user_id/discord_user_id now carry
	// a partial unique index (review #14), so a fixed literal would collide
	// with a row a prior run of this same test left behind in a persistent
	// dev database.
	teamsUserID := member.ID.String() + "-teams"
	discordUserID := member.ID.String() + "-discord"

	member.TeamsUserID = teamsUserID
	member.DiscordUserID = discordUserID
	member.BindingError = "slack API error: channel_not_found"
	member.DeliveryChannel = model.DeliveryChannelDiscord

	if err := store.Update(t.Context(), member); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := store.ByID(t.Context(), member.ID)
	if err != nil {
		t.Fatalf("ByID after update: %v", err)
	}

	if got.TeamsUserID != teamsUserID {
		t.Errorf("TeamsUserID: got %q, want %q", got.TeamsUserID, teamsUserID)
	}

	if got.DiscordUserID != discordUserID {
		t.Errorf("DiscordUserID: got %q, want %q", got.DiscordUserID, discordUserID)
	}

	if got.BindingError != "slack API error: channel_not_found" {
		t.Errorf("BindingError: got %q, want the stored failure text", got.BindingError)
	}

	if got.DeliveryChannel != model.DeliveryChannelDiscord {
		t.Errorf("DeliveryChannel: got %q, want discord", got.DeliveryChannel)
	}

	// Clearing binding_error on the next update (e.g. a successful rebind).
	member.BindingError = ""

	if err := store.Update(t.Context(), member); err != nil {
		t.Fatalf("second Update: %v", err)
	}

	cleared, err := store.ByID(t.Context(), member.ID)
	if err != nil {
		t.Fatalf("ByID after clearing: %v", err)
	}

	if cleared.BindingError != "" {
		t.Errorf("BindingError after clear: got %q, want empty", cleared.BindingError)
	}
}

// TestMemberStoreDuplicate proves that creating a member with the same ID returns
// ErrDuplicate.
func TestMemberStoreDuplicate(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewMemberStore(pool)

	member, _ := model.NewMember(uuid.New(), "dup@example.com", "Dup")

	if err := store.Create(t.Context(), member); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	err := store.Create(t.Context(), member)
	if !errors.Is(err, adaptererr.ErrDuplicate) {
		t.Fatalf("second Create: got %v, want ErrDuplicate", err)
	}
}

// TestMemberStoreNotFound proves that ByID on an absent member returns ErrNotFound.
func TestMemberStoreNotFound(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewMemberStore(pool)

	_, err := store.ByID(t.Context(), uuid.New())
	if !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("ByID(absent): got %v, want ErrNotFound", err)
	}
}

// TestMemberStoreByTeamsUserID proves lookup by the Teams AAD object id
// round-trips and that an absent (or empty) id returns ErrNotFound —
// teams_user_id is NOT NULL DEFAULT ”, so an empty lookup must never match
// an arbitrary unbound member.
func TestMemberStoreByTeamsUserID(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewMemberStore(pool)

	id := uuid.New()
	teamsUserID := id.String() // unique per run

	member, _ := model.NewMember(id, id.String()+"@example.test", "Erin")

	if err := store.Create(t.Context(), member); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Create does not persist teams_user_id (it is a bridge binding set
	// post-creation, mirroring TestMemberStoreUpdate_BridgeFields).
	member.TeamsUserID = teamsUserID

	if err := store.Update(t.Context(), member); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := store.ByTeamsUserID(t.Context(), teamsUserID)
	if err != nil {
		t.Fatalf("ByTeamsUserID: %v", err)
	}

	if got.ID != member.ID {
		t.Errorf("ByTeamsUserID: got %v, want %v", got.ID, member.ID)
	}

	if _, err := store.ByTeamsUserID(t.Context(), uuid.NewString()); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("unknown teams user id: got %v, want ErrNotFound", err)
	}

	if _, err := store.ByTeamsUserID(t.Context(), ""); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("empty teams user id: got %v, want ErrNotFound (never match an unbound member)", err)
	}
}

// TestMemberStoreOffboardFreesDiscordBinding proves the reported chat-binding
// bug is fixed: a member holding discord_user_id=X that is offboarded (Offboard →
// status 'removed') has its binding columns cleared, and a *different* account
// can then bind the same discord_user_id=X without tripping the partial unique
// index (which now also excludes dead statuses).
func TestMemberStoreOffboardFreesDiscordBinding(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewMemberStore(pool)

	// Unique-per-run id so a persistent dev DB from an earlier run can't collide.
	discordUserID := uuid.NewString()

	// First member binds discord_user_id=X.
	first, _ := model.NewMember(uuid.New(), uuid.NewString()+"@example.test", "Grace")
	if err := store.Create(t.Context(), first); err != nil {
		t.Fatalf("Create first: %v", err)
	}

	first.DiscordUserID = discordUserID
	first.TelegramChatID = uuid.NewString()
	first.DeliveryChannel = model.DeliveryChannelDiscord
	if err := store.Update(t.Context(), first); err != nil {
		t.Fatalf("Update first (bind): %v", err)
	}

	orgID := testsupport.SeedOrganization(t, pool)
	projectID := testsupport.SeedProjectInOrg(t, pool, orgID)
	addToProject(t, pool, projectID, first.ID)

	// Sanity: a second live member binding the same id must fail (uniqueness holds
	// for live rows).
	rival, _ := model.NewMember(uuid.New(), uuid.NewString()+"@example.test", "Rival")
	if err := store.Create(t.Context(), rival); err != nil {
		t.Fatalf("Create rival: %v", err)
	}

	rival.DiscordUserID = discordUserID
	if err := store.Update(t.Context(), rival); !errors.Is(err, adaptererr.ErrDuplicate) {
		t.Fatalf("Update rival (collide with live): got %v, want ErrDuplicate", err)
	}

	// Offboard the first member (soft-remove). This must clear its bindings.
	first.Status = model.MemberStatusRemoved
	if err := store.OffboardFromOrg(t.Context(), first, orgID); err != nil {
		t.Fatalf("Offboard first: %v", err)
	}

	clearedRow, err := store.ByID(t.Context(), first.ID)
	if err != nil {
		t.Fatalf("ByID first after offboard: %v", err)
	}

	if clearedRow.Status != model.MemberStatusRemoved {
		t.Errorf("Status: got %q, want removed", clearedRow.Status)
	}

	if clearedRow.DiscordUserID != "" {
		t.Errorf("DiscordUserID after offboard: got %q, want empty", clearedRow.DiscordUserID)
	}

	if clearedRow.TelegramChatID != "" {
		t.Errorf("TelegramChatID after offboard: got %q, want empty", clearedRow.TelegramChatID)
	}

	if clearedRow.DeliveryChannel != model.DeliveryChannelDashboard {
		t.Errorf("DeliveryChannel after offboard: got %q, want dashboard", clearedRow.DeliveryChannel)
	}

	// The freed id can now be bound by a different account's member.
	newcomer, _ := model.NewMember(uuid.New(), uuid.NewString()+"@example.test", "Heidi")
	if err := store.Create(t.Context(), newcomer); err != nil {
		t.Fatalf("Create newcomer: %v", err)
	}

	newcomer.DiscordUserID = discordUserID
	if err := store.Update(t.Context(), newcomer); err != nil {
		t.Fatalf("Update newcomer (rebind freed id): %v — want no unique violation", err)
	}

	got, err := store.ByDiscordUserID(t.Context(), discordUserID)
	if err != nil {
		t.Fatalf("ByDiscordUserID after rebind: %v", err)
	}

	if got.ID != newcomer.ID {
		t.Errorf("ByDiscordUserID resolves to %v, want newcomer %v", got.ID, newcomer.ID)
	}
}

// TestMemberStoreByDiscordUserID mirrors TestMemberStoreByTeamsUserID for the
// Discord binding.
func TestMemberStoreByDiscordUserID(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewMemberStore(pool)

	id := uuid.New()
	discordUserID := id.String()

	member, _ := model.NewMember(id, id.String()+"@example.test", "Frank")

	if err := store.Create(t.Context(), member); err != nil {
		t.Fatalf("Create: %v", err)
	}

	member.DiscordUserID = discordUserID

	if err := store.Update(t.Context(), member); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := store.ByDiscordUserID(t.Context(), discordUserID)
	if err != nil {
		t.Fatalf("ByDiscordUserID: %v", err)
	}

	if got.ID != member.ID {
		t.Errorf("ByDiscordUserID: got %v, want %v", got.ID, member.ID)
	}

	if _, err := store.ByDiscordUserID(t.Context(), uuid.NewString()); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("unknown discord user id: got %v, want ErrNotFound", err)
	}

	if _, err := store.ByDiscordUserID(t.Context(), ""); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("empty discord user id: got %v, want ErrNotFound (never match an unbound member)", err)
	}
}
