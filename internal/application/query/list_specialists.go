// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"

	"github.com/google/uuid"
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
		return nil, translateReadError(err, "project_members")
	}

	memberIDs := make([]uuid.UUID, len(memberships))
	for i, membership := range memberships {
		memberIDs[i] = membership.MemberID
	}

	members, err := h.memberReader.ReadMembers(ctx, memberIDs)
	if err != nil {
		return nil, translateReadError(err, "members")
	}

	online, err := h.presenceReader.ReadOnlineByMembers(ctx, memberIDs)
	if err != nil {
		online = map[uuid.UUID]bool{}
	}

	var experts []Expert

	for _, m := range memberships {
		member, ok := members[m.MemberID]
		if !ok {
			continue
		}

		// Only routable (active) members belong in the directory: a pending,
		// on_leave, deactivated, suspended, invited, removed or purged member
		// cannot be routed to, so surfacing it would mislead the agent.
		if !member.Status.Routable() {
			continue
		}

		experts = append(experts, Expert{
			MemberID:        m.MemberID,
			DisplayName:     member.DisplayName,
			Email:           member.Email,
			Status:          member.Status,
			InvitedAt:       member.CreatedAt,
			Domains:         m.Domains,
			Online:          online[m.MemberID],
			DeliveryChannel: member.DeliveryChannel,
		})
	}

	if experts == nil {
		return []Expert{}, nil
	}

	return experts, nil
}
