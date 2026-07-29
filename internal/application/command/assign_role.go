// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/errs"
)

// AssignRoleCommand sets a member's expertise tags on a project. Post-Part-2 it
// no longer changes a permission role (that concept is retired); it replaces the
// member's domains wholesale (set-tags semantics). The command name is retained
// for wire/RPC stability.
type AssignRoleCommand struct {
	// ProjectID is the project in which the member's tags are set.
	ProjectID uuid.UUID
	// MemberID identifies the member whose expertise tags are being set.
	MemberID uuid.UUID
	// Domains are the expertise tags to apply; they replace the member's
	// existing domains on the project.
	Domains []string `exhaustruct:"optional"`
}

// AssignRoleHandler handles AssignRoleCommand.
type AssignRoleHandler struct {
	projectRepo repository.ProjectRepository
	bus         eventBus
}

// MustNewAssignRoleHandler builds a handler. It panics on nil
// dependencies.
func MustNewAssignRoleHandler(
	projectRepo repository.ProjectRepository,
	bus eventBus,
) AssignRoleHandler {
	if projectRepo == nil {
		panic("AssignRoleHandler requires a non-nil ProjectRepository")
	}

	if bus == nil {
		panic("AssignRoleHandler requires a non-nil eventBus")
	}

	return AssignRoleHandler{projectRepo: projectRepo, bus: bus}
}

// Handle replaces the member's expertise tags on the project (UPDATE, not
// INSERT — the member is already enrolled) and emits MemberLifecycle(ROLE_CHANGED)
// as an audit trail. Returns ErrNotFound when the member is not enrolled.
func (h AssignRoleHandler) Handle(ctx context.Context, cmd AssignRoleCommand) error {
	if err := h.projectRepo.SetMemberDomains(ctx, cmd.ProjectID, cmd.MemberID, cmd.Domains); err != nil {
		return translateErr(err, "project_membership")
	}

	// The membership no longer carries a permission role (Part 2); the lifecycle
	// event's Role field is left unspecified. ROLE_CHANGED is retained as the
	// closest existing transition for the tag-change audit trail.
	if _, err := h.bus.Publish(ctx, &orakov1.Envelope{
		ProjectId: cmd.ProjectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_MEMBER_LIFECYCLE,
		Payload: &orakov1.Envelope_MemberLifecycle{
			MemberLifecycle: &orakov1.MemberLifecycle{
				MemberId:   cmd.MemberID.String(),
				ProjectId:  cmd.ProjectID.String(),
				Transition: orakov1.MemberTransition_MEMBER_TRANSITION_ROLE_CHANGED,
			},
		},
	}); err != nil {
		return errs.InternalError{Err: fmt.Errorf("publishing member_lifecycle(role_changed): %w", err)}
	}

	return nil
}
