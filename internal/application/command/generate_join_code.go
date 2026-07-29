// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// GenerateJoinCodeCommand rotates the org's shareable join code: it revokes any
// prior live code and mints a fresh one scoped to a project. OrgID and
// ActorMemberID come from the authenticated caller (never a request field);
// ProjectID is optional — the nil UUID targets the org's default project.
type GenerateJoinCodeCommand struct {
	OrgID         uuid.UUID
	ProjectID     uuid.UUID `exhaustruct:"optional"`
	ActorMemberID uuid.UUID `exhaustruct:"optional"`
}

// GenerateJoinCodeResult is the freshly minted code and the project it admits to.
type GenerateJoinCodeResult struct {
	Code        string
	ProjectID   uuid.UUID
	ProjectName string
}

// joinCodeMinter mints a fresh live join code for an org, revoking any prior
// live code in the same transaction. *identity.JoinTokenStore satisfies it.
type joinCodeMinter interface {
	CreateJoinToken(ctx context.Context, orgID, projectID, createdByMemberID uuid.UUID) (string, error)
}

// joinCodeProjectResolver resolves the target project a code admits to: an
// explicit one (verified to belong to the org) or the org's default project.
// *identity.ProjectStore satisfies it.
type joinCodeProjectResolver interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Project, error)
	DefaultProjectByOrg(ctx context.Context, orgID uuid.UUID) (model.Project, error)
}

// GenerateJoinCodeHandler handles GenerateJoinCodeCommand.
type GenerateJoinCodeHandler struct {
	tokens   joinCodeMinter
	projects joinCodeProjectResolver
}

// MustNewGenerateJoinCodeHandler builds the handler. It panics on a
// nil dependency, per project convention.
func MustNewGenerateJoinCodeHandler(tokens joinCodeMinter, projects joinCodeProjectResolver) GenerateJoinCodeHandler {
	if tokens == nil || projects == nil {
		panic("GenerateJoinCodeHandler requires a non-nil minter and project resolver")
	}

	return GenerateJoinCodeHandler{tokens: tokens, projects: projects}
}

// Handle resolves the target project, then rotates the org's join code.
//
// SECURITY: OrgID is the authenticated caller's org (never a request field). An
// explicit ProjectID is verified to belong to that org before the code is
// minted, so an admin cannot mint a code admitting strangers into a project of
// another organization.
func (h GenerateJoinCodeHandler) Handle(ctx context.Context, cmd GenerateJoinCodeCommand) (GenerateJoinCodeResult, error) {
	if cmd.OrgID == uuid.Nil {
		return GenerateJoinCodeResult{}, errs.InvalidError{Field: "org_id", Reason: reasonNilUUID}
	}

	project, err := h.resolveProject(ctx, cmd.OrgID, cmd.ProjectID)
	if err != nil {
		return GenerateJoinCodeResult{}, err
	}

	code, err := h.tokens.CreateJoinToken(ctx, cmd.OrgID, project.ID, cmd.ActorMemberID)
	if err != nil {
		return GenerateJoinCodeResult{}, translateErr(err, "join_code")
	}

	return GenerateJoinCodeResult{Code: code, ProjectID: project.ID, ProjectName: project.Name}, nil
}

// resolveProject returns the org's default project when projectID is nil, or the
// named project after confirming it belongs to orgID.
func (h GenerateJoinCodeHandler) resolveProject(ctx context.Context, orgID, projectID uuid.UUID) (model.Project, error) {
	if projectID == uuid.Nil {
		project, err := h.projects.DefaultProjectByOrg(ctx, orgID)
		if err != nil {
			return model.Project{}, translateErr(err, "project")
		}

		return project, nil
	}

	project, err := h.projects.ByID(ctx, projectID)
	if err != nil {
		return model.Project{}, translateErr(err, "project")
	}

	if project.OrgID != orgID {
		return model.Project{}, errs.ForbiddenError{Action: "generate a join code for a project outside your organization"}
	}

	return project, nil
}
