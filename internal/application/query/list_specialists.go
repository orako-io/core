// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"errors"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
)

// ListExpertsQuery is the input for listing experts in a project.
type ListExpertsQuery struct {
	// ProjectID scopes the result to a project (RBAC: caller must be a member).
	ProjectID uuid.UUID
}

// ListExpertsHandler handles ListExpertsQuery.
type ListExpertsHandler struct {
	projectReader  ProjectReader
	memberReader   MemberReader
	presenceReader PresenceReader
}

// MustNewListExpertsHandler builds a handler. It panics on nil
// dependencies.
func MustNewListExpertsHandler(
	projectReader ProjectReader,
	memberReader MemberReader,
	presenceReader PresenceReader,
) ListExpertsHandler {
	if projectReader == nil {
		panic("ListExpertsHandler requires a non-nil ProjectReader")
	}

	if memberReader == nil {
		panic("ListExpertsHandler requires a non-nil MemberReader")
	}

	if presenceReader == nil {
		panic("ListExpertsHandler requires a non-nil PresenceReader")
	}

	return ListExpertsHandler{
		projectReader:  projectReader,
		memberReader:   memberReader,
		presenceReader: presenceReader,
	}
}

// Handle returns every ROUTABLE project member with their expertise tags
// (domains) and presence. Post-Part-2 there is no permission role to filter on:
// every active member is a potential responder, and the agent routes on domains.
// Only routable (active) members are returned — the directory must never offer
// the agent a target it cannot actually reach. That excludes pending self-join
// members (awaiting admin approval), on_leave, deactivated, suspended, invited,
// removed and purged, matching the direct-ask (ensureDirectTargetRoutable) and
// domain-dispatch (projectMembersByDomains) routing filters. Presence is a
// poll-on-demand hint.
func (h ListExpertsHandler) Handle(ctx context.Context, q ListExpertsQuery) ([]Expert, error) {
	memberships, err := h.projectReader.MembersByProject(ctx, q.ProjectID)
	if err != nil {
		return nil, err
	}

	var experts []Expert

	for _, m := range memberships {
		member, err := h.memberReader.ReadMember(ctx, m.MemberID)
		if err != nil {
			if errors.Is(err, adaptererr.ErrNotFound) {
				continue
			}

			return nil, err
		}

		// Only routable (active) members belong in the directory: a pending,
		// on_leave, deactivated, suspended, invited, removed or purged member
		// cannot be routed to, so surfacing it would mislead the agent.
		if !member.Status.Routable() {
			continue
		}

		// Errors from presence (including ErrNotFound) are silently swallowed:
		// presence is a weak hint (PRD §6), not a hard requirement.
		online := false
		if on, err := h.presenceReader.ReadOnline(ctx, m.MemberID); err == nil {
			online = on
		}

		experts = append(experts, Expert{
			MemberID:        m.MemberID,
			DisplayName:     member.DisplayName,
			Email:           member.Email,
			Status:          member.Status,
			InvitedAt:       member.CreatedAt,
			Domains:         m.Domains,
			Online:          online,
			DeliveryChannel: member.DeliveryChannel,
		})
	}

	if experts == nil {
		return []Expert{}, nil
	}

	return experts, nil
}
