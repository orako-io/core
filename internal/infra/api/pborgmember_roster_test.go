// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/query"
)

// TestPbOrgMemberRoster_RedactsSensitivePII proves the non-admin roster
// projection drops every sensitive field (email, external chat IDs, delivery
// channel, binding error) while preserving the identity, expertise and
// membership tier (org-admin flag) a member needs to route a question and read
// the Admin/Team tiers. Guards the fix for un-gating ListMembers, which must NOT
// leak PII to non-admins.
func TestPbOrgMemberRoster_RedactsSensitivePII(t *testing.T) {
	t.Parallel()

	view := query.OrgMemberView{
		MemberID:        uuid.New(),
		Email:           "alice@example.com",
		DisplayName:     "Alice",
		FirstName:       "Alice",
		LastName:        "Ng",
		GitHandle:       "alice",
		DeliveryChannel: "slack",
		SlackUserID:     "U123",
		TeamsUserID:     "T456",
		TelegramChatID:  "789",
		DiscordUserID:   "D012",
		BindingError:    "delivery failed on slack",
		Status:          "active",
		IsOrgAdmin:      true,
	}

	pb := pbOrgMemberRoster(view)

	// Preserved: identity + expertise the roster needs.
	if pb.GetDisplayName() != "Alice" || pb.GetFirstName() != "Alice" || pb.GetLastName() != "Ng" {
		t.Errorf("roster dropped identity: %+v", pb)
	}

	if pb.GetGitHandle() != "alice" {
		t.Errorf("roster dropped git handle: %q", pb.GetGitHandle())
	}

	// Redacted: every sensitive field must be empty/false.
	if pb.GetEmail() != "" {
		t.Errorf("email leaked to non-admin roster: %q", pb.GetEmail())
	}

	if pb.GetSlackUserId() != "" || pb.GetTeamsUserId() != "" || pb.GetTelegramChatId() != "" || pb.GetDiscordUserId() != "" {
		t.Errorf("external chat IDs leaked to non-admin roster: %+v", pb)
	}

	if pb.GetDeliveryChannel() != "" {
		t.Errorf("delivery channel leaked to non-admin roster: %q", pb.GetDeliveryChannel())
	}

	if pb.GetBindingError() != "" {
		t.Errorf("binding error leaked to non-admin roster: %q", pb.GetBindingError())
	}

	// Preserved: the membership tier (Admin/Team) is shown to every member.
	if !pb.GetIsOrgAdmin() {
		t.Error("is_org_admin (membership tier) must be preserved for the roster view")
	}
}
