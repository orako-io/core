// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"

	"github.com/google/uuid"
)

// ListProjectsQuery is the input for listing projects the caller is a member of.
// The caller identity comes from the auth context; the handler receives it as
// CallerMemberID so the transport layer can extract it once.
type ListProjectsQuery struct {
	// CallerMemberID is the member whose project memberships are returned.
	// Populated by the transport layer from the RBAC context.
	CallerMemberID uuid.UUID
	// OrgID, when non-nil, restricts the result to the caller's active org so the
	// dashboard project switcher shows one org at a time. Nil returns every
	// project (self-host single-org, or a caller with no resolved org).
	OrgID uuid.UUID `exhaustruct:"optional"`
}

// ListProjectsHandler handles ListProjectsQuery.
type ListProjectsHandler struct {
	reader ProjectsByMemberReader
}

// MustNewListProjectsHandler builds a handler. It panics on nil
// dependencies.
func MustNewListProjectsHandler(reader ProjectsByMemberReader) ListProjectsHandler {
	if reader == nil {
		panic("ListProjectsHandler requires a non-nil ProjectsByMemberReader")
	}

	return ListProjectsHandler{reader: reader}
}

// Handle returns all projects where the caller holds any role, mapped to
// ProjectSummary DTOs. An empty slice is returned (not nil) when the caller
// has no memberships.
func (h ListProjectsHandler) Handle(ctx context.Context, q ListProjectsQuery) ([]ProjectSummary, error) {
	projects, err := h.reader.ProjectsByMember(ctx, q.CallerMemberID)
	if err != nil {
		return nil, err
	}

	summaries := make([]ProjectSummary, 0, len(projects))

	for _, p := range projects {
		if q.OrgID != uuid.Nil && p.OrgID != q.OrgID {
			continue
		}

		// Archived projects are a reversible freeze: they drop out of every
		// read by default, including this one. There is no include_archived
		// escape hatch on ListProjects — only ListProjectsDetailed (the
		// Projects tab) opts back in.
		if p.Archived {
			continue
		}

		summaries = append(summaries, ProjectSummary{
			ID:    p.ID,
			Name:  p.Name,
			Role:  p.Role,
			OrgID: p.OrgID,
		})
	}

	return summaries, nil
}
