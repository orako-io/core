// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/errs"
)

// SetOwnExpertiseHandler sets the caller's expertise tags within one org.
type SetOwnExpertiseHandler struct {
	projectRepo repository.ProjectRepository
}

// SetOwnExpertiseCommand updates a member's expertise within one organization.
type SetOwnExpertiseCommand struct {
	OrgID    uuid.UUID
	MemberID uuid.UUID
	Domains  []string
}

// MustNewSetOwnExpertiseHandler builds a handler. It panics on a nil
// dependency.
func MustNewSetOwnExpertiseHandler(projectRepo repository.ProjectRepository) SetOwnExpertiseHandler {
	if projectRepo == nil {
		panic("SetOwnExpertiseHandler requires a non-nil ProjectRepository")
	}

	return SetOwnExpertiseHandler{projectRepo: projectRepo}
}

// Handle normalizes and replaces the member's org-scoped expertise tags.
func (h SetOwnExpertiseHandler) Handle(ctx context.Context, cmd SetOwnExpertiseCommand) error {
	if cmd.OrgID == uuid.Nil {
		return errs.InvalidError{Field: fieldOrgID, Reason: reasonNoOrgResolved}
	}

	if cmd.MemberID == uuid.Nil {
		return errs.InvalidError{Field: fieldMemberID, Reason: reasonNilUUID}
	}

	if err := h.projectRepo.SetDomainsForMemberInOrg(ctx, cmd.OrgID, cmd.MemberID, model.NormalizeTags(cmd.Domains)); err != nil {
		return translateErr(err, "project_membership")
	}

	return nil
}
