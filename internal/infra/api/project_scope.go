// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/repository"
)

// scopeProjects intersects a member's memberships with a token's project scope,
// preserving the token's order so the first element is the primary project. An
// empty scope means "all the member's projects" (the legacy, unscoped default)
// and returns memberships unchanged. A scoped id the member does not — or no
// longer — belongs to is dropped: a stale or forged token scope can never reach
// a project the member isn't a member of, and the check re-runs every call so
// it stays correct if membership changes after the token was issued.
func scopeProjects(memberships []repository.ProjectWithRole, scope []uuid.UUID) []repository.ProjectWithRole {
	if len(scope) == 0 {
		return memberships
	}

	byID := make(map[uuid.UUID]repository.ProjectWithRole, len(memberships))
	for _, m := range memberships {
		byID[m.ID] = m
	}

	out := make([]repository.ProjectWithRole, 0, len(scope))

	for _, id := range scope {
		if m, ok := byID[id]; ok {
			out = append(out, m)
		}
	}

	return out
}

// projectIDsOf extracts the project ids in order.
func projectIDsOf(memberships []repository.ProjectWithRole) []uuid.UUID {
	out := make([]uuid.UUID, len(memberships))
	for i, m := range memberships {
		out[i] = m.ID
	}

	return out
}
