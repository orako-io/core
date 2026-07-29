// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"time"

	"github.com/google/uuid"
)

// OrgMemberView is the read model for the admin org roster (ListMembers /
// GetOrgMember). It is a flat, query-owned projection — never the domain Member
// aggregate — so the read side stays decoupled from the write model. Domain
// enums (delivery channel, status) are carried as plain strings; the adapter
// that reads the roster fills this in and the transport maps it to the wire.
type OrgMemberView struct {
	MemberID        uuid.UUID
	Email           string
	FirstName       string
	LastName        string
	DisplayName     string
	GitHandle       string
	DeliveryChannel string
	SlackUserID     string
	TeamsUserID     string
	TelegramChatID  string
	DiscordUserID   string
	BindingError    string
	Status          string
	ReturnDate      *time.Time `exhaustruct:"optional"`
	IsOrgAdmin      bool
	External        bool
	Projects        []ProjectExpertise `exhaustruct:"optional"`
}

// ProjectExpertise is a member's expertise tags within one project, as seen by
// the roster read model.
type ProjectExpertise struct {
	ProjectID uuid.UUID
	Domains   []string
}
