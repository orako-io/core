// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/errs"
)

// RemoveMemberCommand is the input for archiving or purging a member.
//
// Default (Purge = false): soft-delete. The member record is set to
// MemberStatusRemoved; their answers are retained with full attribution; the
// member is no longer pinged as a responder.
//
// Purge = true: hard-delete of PII only, on a legal/GDPR request. The member
// record is set to MemberStatusPurged; personal data (email, display_name,
// slack_user_id) is cleared; their answers stay (anonymized to "former
// contributor").
type RemoveMemberCommand struct {
	// MemberID identifies the member to remove.
	MemberID uuid.UUID
	// OrgID scopes the removal to the caller's organization.
	OrgID uuid.UUID
	// ProjectID is used for scoping the MemberLifecycle event.
	ProjectID uuid.UUID
	// Purge triggers the PII hard-delete path when true. Default is archive.
	Purge bool
}

// RemoveMemberHandler handles RemoveMemberCommand.
type RemoveMemberHandler struct {
	memberRepo repository.MemberRepository
	bus        eventBus
}

// MustNewRemoveMemberHandler builds a handler. It panics on nil
// dependencies.
func MustNewRemoveMemberHandler(
	memberRepo repository.MemberRepository,
	bus eventBus,
) RemoveMemberHandler {
	if memberRepo == nil {
		panic("RemoveMemberHandler requires a non-nil MemberRepository")
	}

	if bus == nil {
		panic("RemoveMemberHandler requires a non-nil eventBus")
	}

	return RemoveMemberHandler{memberRepo: memberRepo, bus: bus}
}

// Handle archives (default) or purges PII (Purge = true) for the member and
// emits MemberLifecycle(REMOVED) or MemberLifecycle(PURGED).
func (h RemoveMemberHandler) Handle(ctx context.Context, cmd RemoveMemberCommand) error {
	member, err := h.memberRepo.ByID(ctx, cmd.MemberID)
	if err != nil {
		return translateErr(err, "member")
	}

	transition := orakov1.MemberTransition_MEMBER_TRANSITION_REMOVED

	if cmd.Purge {
		// PII hard-delete: clear personal data, keep the record for answer
		// attribution (anonymized). Their answer rows are not touched.
		member.Status = model.MemberStatusPurged
		member.Email = ""
		member.DisplayName = ""
		transition = orakov1.MemberTransition_MEMBER_TRANSITION_PURGED
	} else {
		// Soft-delete (archive): revoke access, but keep all data.
		member.Status = model.MemberStatusRemoved
	}

	// Remove only the links in the caller's organization. The repository
	// terminalizes the shared member record only when no other organization
	// still references it.
	if err = h.memberRepo.OffboardFromOrg(ctx, member, cmd.OrgID); err != nil {
		return translateErr(err, "member")
	}

	if _, err = h.bus.Publish(ctx, &orakov1.Envelope{
		ProjectId: cmd.ProjectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_MEMBER_LIFECYCLE,
		Payload: &orakov1.Envelope_MemberLifecycle{
			MemberLifecycle: &orakov1.MemberLifecycle{
				MemberId:   cmd.MemberID.String(),
				ProjectId:  cmd.ProjectID.String(),
				Transition: transition,
			},
		},
	}); err != nil {
		return errs.InternalError{Err: fmt.Errorf("publishing member_lifecycle: %w", err)}
	}

	return nil
}
