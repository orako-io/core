// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// OrgRole is an account's role within an organization (org-scoped RBAC),
// distinct from the project-scoped Role. Mirrors the `role` column of org_members.
type OrgRole string

// Recognized org role values.
const (
	// OrgRoleAdmin can manage the org: settings, members, projects, billing.
	// The account that creates an org becomes its admin.
	OrgRoleAdmin OrgRole = "admin"
	// OrgRoleMember is a regular org participant.
	OrgRoleMember OrgRole = "member"
)

// Valid reports whether r is a recognized org role.
func (r OrgRole) Valid() bool {
	switch r {
	case OrgRoleAdmin, OrgRoleMember:
		return true
	default:
		return false
	}
}

// Organization is the tenant boundary above projects.
type Organization struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewOrganization builds an Organization ready for insertion, validating its
// invariants.
func NewOrganization(id uuid.UUID, name string) (Organization, error) {
	if id == uuid.Nil {
		return Organization{}, errs.InvalidError{Field: "id", Reason: nilUUIDReason}
	}

	if strings.TrimSpace(name) == "" {
		return Organization{}, errs.InvalidError{Field: "name", Reason: emptyReason}
	}

	now := time.Now().UTC()

	return Organization{ID: id, Name: name, CreatedAt: now, UpdatedAt: now}, nil
}

// OrgMembership is an account's membership in an organization, with its role.
type OrgMembership struct {
	OrgID     uuid.UUID
	AccountID uuid.UUID
	Role      OrgRole
	CreatedAt time.Time `exhaustruct:"optional"`
}
