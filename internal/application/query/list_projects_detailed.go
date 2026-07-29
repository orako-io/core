// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/errs"
)

// ListProjectsDetailedQuery is the input for the Projects tab: every project
// in the caller's org with its identity, archived status, aggregate stats, and
// per-provider alert routing.
type ListProjectsDetailedQuery struct {
	OrgID uuid.UUID
	// IncludeArchived, when true, includes archived projects. Default (false)
	// excludes them — the same "archived drops out of reads by default"
	// convention as every other read, with this RPC being the one opt-in.
	IncludeArchived bool `exhaustruct:"optional"`
}

// ListProjectsDetailedHandler handles ListProjectsDetailedQuery.
type ListProjectsDetailedHandler struct {
	projects ProjectsDetailedReader
	routing  projectAlertChannelsReader
}

type projectAlertChannelsReader interface {
	ConfiguredProvidersWithAlertChannels(ctx context.Context, projectIDs []uuid.UUID) (map[uuid.UUID][]service.ProviderAlertChannel, error)
}

// MustNewListProjectsDetailedHandler builds a handler. It panics on
// nil dependencies.
func MustNewListProjectsDetailedHandler(
	projects ProjectsDetailedReader,
	routing projectAlertChannelsReader,
) ListProjectsDetailedHandler {
	if projects == nil {
		panic("ListProjectsDetailedHandler requires a non-nil ProjectsDetailedReader")
	}

	if routing == nil {
		panic("ListProjectsDetailedHandler requires a non-nil ProviderAlertChannelsReader")
	}

	return ListProjectsDetailedHandler{projects: projects, routing: routing}
}

// Handle returns every project in scope with its stats and per-provider alert
// routing. An empty slice is returned (not nil) when the org has no projects.
func (h ListProjectsDetailedHandler) Handle(ctx context.Context, q ListProjectsDetailedQuery) ([]ProjectDetail, error) {
	rows, err := h.projects.ProjectsDetailedByOrg(ctx, q.OrgID, q.IncludeArchived)
	if err != nil {
		return nil, errs.InternalError{Err: fmt.Errorf("listing detailed projects: %w", err)}
	}

	details := make([]ProjectDetail, len(rows))

	projectIDs := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		projectIDs[i] = row.ID
	}

	routing, err := h.routing.ConfiguredProvidersWithAlertChannels(ctx, projectIDs)
	if err != nil {
		return nil, errs.InternalError{Err: fmt.Errorf("listing provider alert channels: %w", err)}
	}

	for i, r := range rows {
		details[i] = ProjectDetail{
			ID:                r.ID,
			Name:              r.Name,
			Archived:          r.Archived,
			MemberCount:       r.MemberCount,
			ConversationCount: r.ConversationCount,
			ResolvedCount:     r.ResolvedCount,
			Routing:           routing[r.ID],
		}
	}

	return details, nil
}
