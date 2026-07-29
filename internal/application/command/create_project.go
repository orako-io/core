// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/errs"
)

// CreateProjectCommand is the input for creating a new project in an org. The
// project is created inside the caller's organization and the caller is added
// as a member so it appears in their project list.
type CreateProjectCommand struct {
	// Name is the human-readable label for the project.
	Name string
	// OrgID is the organization the project belongs to (the caller's org).
	OrgID uuid.UUID
	// CreatorMemberID is the caller's member row, added to the new project.
	CreatorMemberID uuid.UUID
}

// CreateProjectResult carries the generated project ID.
type CreateProjectResult struct {
	// ProjectID is the ID of the newly created project.
	ProjectID uuid.UUID
}

// projectProvisioner writes a project and its creator's membership. Both writes
// join the ambient transaction through their context, so the handler composes
// them under one service.Transactor rather than a dedicated atomic-store
// adapter. Satisfied by *identity.ProjectStore.
type projectProvisioner interface {
	CreateInOrg(ctx context.Context, project model.Project) error
	AddMember(ctx context.Context, m repository.ProjectMembership) error
}

// ProjectLimitGate authorizes creating a new project against the edition's
// resource caps. Wired only for the Community edition (see
// application.buildProjectGate); nil means no enforcement.
type ProjectLimitGate interface {
	// AllowNewProject returns a typed error when the org is at its project cap.
	AllowNewProject(ctx context.Context, orgID uuid.UUID) error
}

// CreateProjectHandler handles CreateProjectCommand.
type CreateProjectHandler struct {
	projects projectProvisioner
	txor     service.Transactor
	gate     ProjectLimitGate `exhaustruct:"optional"`
}

// MustNewCreateProjectHandler builds a handler. It panics on nil
// required dependencies. gate is optional: pass nil to disable project-cap
// enforcement (SaaS / Licensed editions).
func MustNewCreateProjectHandler(projects projectProvisioner, txor service.Transactor, gate ProjectLimitGate) CreateProjectHandler {
	if projects == nil {
		panic("CreateProjectHandler requires a non-nil projectProvisioner")
	}

	if txor == nil {
		panic("CreateProjectHandler requires a non-nil service.Transactor")
	}

	return CreateProjectHandler{projects: projects, txor: txor, gate: gate}
}

// Handle creates the project in the caller's org and adds the caller as a
// member, atomically.
func (h CreateProjectHandler) Handle(ctx context.Context, cmd CreateProjectCommand) (CreateProjectResult, error) {
	if cmd.OrgID == uuid.Nil {
		return CreateProjectResult{}, errs.InvalidError{Field: fieldOrgID, Reason: reasonNilUUID}
	}

	if cmd.CreatorMemberID == uuid.Nil {
		return CreateProjectResult{}, errs.InvalidError{Field: "creator_member_id", Reason: reasonNilUUID}
	}

	// Community-edition project cap (nil gate = no enforcement).
	if h.gate != nil {
		if err := h.gate.AllowNewProject(ctx, cmd.OrgID); err != nil {
			return CreateProjectResult{}, err
		}
	}

	id := uuid.New()

	project, err := model.NewProjectInOrg(id, cmd.Name, cmd.OrgID)
	if err != nil {
		return CreateProjectResult{}, err
	}

	if err = h.provision(ctx, project, cmd.CreatorMemberID); err != nil {
		return CreateProjectResult{}, translateErr(err, "project")
	}

	return CreateProjectResult{ProjectID: id}, nil
}

// provision creates the project in its org and adds the creator as a member in
// one transaction. Each store call joins the ambient tx via its context, so the
// project never lands without its creator on it (which would hide it from the
// creator's project list) — replacing the former single-context
// ProjectAtomicStore adapter.
func (h CreateProjectHandler) provision(ctx context.Context, project model.Project, creatorMemberID uuid.UUID) error {
	return h.txor.WithTx(ctx, func(ctx context.Context) error {
		if err := h.projects.CreateInOrg(ctx, project); err != nil {
			return err
		}

		return h.projects.AddMember(ctx, repository.ProjectMembership{
			ProjectID: project.ID,
			MemberID:  creatorMemberID,
			Role:      model.RoleUnspecified,
			Domains:   []string{},
		})
	})
}
