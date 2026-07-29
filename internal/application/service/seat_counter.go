// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"

	"github.com/google/uuid"
)

// SeatCounter reports how many seats an organization is using. A seat is one
// distinct person in the org: every account in the org (org_members) plus every
// external-only member (no login account) participating in the org's projects.
// Counting is an identity concern (satisfied by *identity.OrganizationStore),
// not a billing one — this port stays in core while billing lives in the SaaS
// overlay.
type SeatCounter interface {
	// CountByOrg returns the org's current seat usage.
	CountByOrg(ctx context.Context, orgID uuid.UUID) (int, error)
}
