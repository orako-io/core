// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// Project is the top-level aggregate that groups members with roles and scopes
// events and conversations. It is the post-pivot
// replacement for the collision-era Room.
type Project struct {
	// ID uniquely identifies the project.
	ID uuid.UUID
	// Name is the human-readable label for the project.
	Name string
	// OrgID links the project to its owning organization. The nil UUID means the
	// project predates the org model and has not been backfilled yet. New projects
	// created through CreateOrganization always have a non-nil OrgID.
	OrgID uuid.UUID `exhaustruct:"optional"`
	// CreatedAt is when the project was created.
	CreatedAt time.Time
	// UpdatedAt is when the project was last modified.
	UpdatedAt time.Time
	// ArchivedAt is set when the project is archived (a reversible freeze); nil
	// means active. An archived project keeps its data but drops out of reads,
	// routing and the seat count.
	ArchivedAt *time.Time `exhaustruct:"optional"`
}

// Archived reports whether the project is currently frozen.
func (p Project) Archived() bool { return p.ArchivedAt != nil }

// NewProjectInOrg builds a Project linked to an organization, validating every
// invariant including that orgID is not nil. Use this constructor when the project
// must be attached to an org at creation time (e.g. the global project created
// by CreateOrganization).
func NewProjectInOrg(id uuid.UUID, name string, orgID uuid.UUID) (Project, error) {
	if id == uuid.Nil {
		return Project{}, errs.InvalidError{Field: "id", Reason: nilUUIDReason}
	}

	if strings.TrimSpace(name) == "" {
		return Project{}, errs.InvalidError{Field: "name", Reason: emptyReason}
	}

	if orgID == uuid.Nil {
		return Project{}, errs.InvalidError{Field: "org_id", Reason: nilUUIDReason}
	}

	now := time.Now().UTC()

	return Project{
		ID:        id,
		Name:      name,
		OrgID:     orgID,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}
