// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
)

// SetOwnExpertiseHandler sets the caller's own expertise tags (domains) across
// every project they belong to. It backs the self-serve onboarding path: unlike
// AssignRole (org-admin gated, one project, another member), this is invoked by
// a member on themselves. The caller identity is supplied by the server from
// the auth token — never a request field.
type SetOwnExpertiseHandler struct {
	projectRepo repository.ProjectRepository
}

// MustNewSetOwnExpertiseHandler builds a handler. It panics on a nil
// dependency.
func MustNewSetOwnExpertiseHandler(projectRepo repository.ProjectRepository) SetOwnExpertiseHandler {
	if projectRepo == nil {
		panic("SetOwnExpertiseHandler requires a non-nil ProjectRepository")
	}

	return SetOwnExpertiseHandler{projectRepo: projectRepo}
}

// Handle normalizes the domains (trim/lowercase/dedup, consistent with the rest
// of the tag surface) and replaces the member's domains on all their project
// memberships. memberID is the authenticated caller; it must be non-nil (the
// server rejects uuid.Nil before reaching here).
func (h SetOwnExpertiseHandler) Handle(ctx context.Context, memberID uuid.UUID, domains []string) error {
	if err := h.projectRepo.SetDomainsForMember(ctx, memberID, model.NormalizeTags(domains)); err != nil {
		return translateErr(err, "project_membership")
	}

	return nil
}
