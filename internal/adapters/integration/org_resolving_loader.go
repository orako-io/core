// SPDX-License-Identifier: AGPL-3.0-or-later

package integration

import (
	"context"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
)

// projectOrgReader resolves a project to its owning org.
type projectOrgReader interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Project, error)
}

// orgCredLoader loads provider credentials for an (org, kind).
type orgCredLoader interface {
	LoadProvider(ctx context.Context, orgID uuid.UUID, kind string) ([]byte, error)
}

// OrgResolvingProviderLoader adapts org-level provider credentials to the
// project-keyed provider.ProviderLoader seam the registry reads through: it
// resolves a project to its org, then loads the org's credentials for the
// kind. This lets integrations live at the org level (one Slack per org,
// DMing people) while the registry and every delivery call site keep their
// existing (project, kind) shape.
//
// It is a drop-in replacement for the project-keyed integration.ProjectProviderStore loader
// but is intentionally not wired into the composition root yet — activating it
// changes how live delivery resolves credentials, which must be verified before
// deploy.
type OrgResolvingProviderLoader struct {
	projects projectOrgReader
	orgCreds orgCredLoader
}

// NewOrgResolvingProviderLoader builds the loader over a project reader and an
// org-credential loader (e.g. identity.ProjectStore and integration.OrgProviderStore).
func NewOrgResolvingProviderLoader(projects projectOrgReader, orgCreds orgCredLoader) *OrgResolvingProviderLoader {
	return &OrgResolvingProviderLoader{projects: projects, orgCreds: orgCreds}
}

// LoadProvider resolves projectID to its org and returns the org's credentials
// for kind. Returns adaptererr.ErrNotFound when the project has no org or the
// org has no provider of that kind — the same signal the registry read-through
// treats as "no provider configured".
func (l *OrgResolvingProviderLoader) LoadProvider(ctx context.Context, projectID uuid.UUID, kind string) ([]byte, error) {
	project, err := l.projects.ByID(ctx, projectID)
	if err != nil {
		return nil, err
	}

	if project.OrgID == uuid.Nil {
		return nil, adaptererr.ErrNotFound
	}

	return l.orgCreds.LoadProvider(ctx, project.OrgID, kind)
}
