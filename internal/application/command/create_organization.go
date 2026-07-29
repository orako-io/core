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

// CreateOrganizationCommand is the input for creating an organization.
type CreateOrganizationCommand struct {
	// Name is the human-readable label for the organization.
	Name string
	// CreatorAccountID is the account that becomes the org admin and is linked as
	// an active member of the auto-created global project.
	// Must not be the nil UUID; the account must already exist.
	CreatorAccountID uuid.UUID
}

// CreateOrganizationResult carries the IDs produced by the use case.
type CreateOrganizationResult struct {
	// OrgID is the newly created organization.
	OrgID uuid.UUID
	// GlobalProjectID is the auto-created global project linked to the org.
	GlobalProjectID uuid.UUID
}

// creatorAccountReader resolves the creator account so the owner member is
// created with the account's email/display name. Satisfied by *identity.AccountStore.
type creatorAccountReader interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Account, error)
}

// OrgLimitGate authorizes creating a new organization against the edition's
// resource caps. It is wired only for the Community edition (see
// application.buildOrgGate); nil means no enforcement (SaaS / Licensed).
type OrgLimitGate interface {
	// AllowNewOrg returns a typed error when the instance is at its org cap.
	AllowNewOrg(ctx context.Context) error
}

// The four narrow write ports the handler composes into one transaction (via
// service.Transactor), replacing the OrganizationAtomicStore adapter.
type (
	orgWriter interface {
		Create(ctx context.Context, org model.Organization) error
		AddMember(ctx context.Context, m model.OrgMembership) error
	}
	globalProjectWriter interface {
		CreateInOrg(ctx context.Context, project model.Project) error
		AddMember(ctx context.Context, m repository.ProjectMembership) error
	}
	creatorMemberWriter interface {
		CreateWithAccount(ctx context.Context, member model.Member) error
	}
	// OrgCreatedHook runs extra writes inside the org-creation transaction when
	// a new organization is created. It is the extension seam the SaaS overlay
	// uses to open a billing trial; the community build passes nil, so core
	// creates no billing state and carries no billing types.
	OrgCreatedHook interface {
		OnOrgCreated(ctx context.Context, orgID, creatorAccountID uuid.UUID) error
	}
)

// CreateOrganizationHandler handles CreateOrganizationCommand.
type CreateOrganizationHandler struct {
	orgs      orgWriter
	projects  globalProjectWriter
	members   creatorMemberWriter
	txor      service.Transactor
	accounts  creatorAccountReader
	onCreated OrgCreatedHook `exhaustruct:"optional"`
	gate      OrgLimitGate   `exhaustruct:"optional"`
}

// MustNewCreateOrganizationHandler builds the handler. It panics on
// nil required dependencies per project convention. onCreated and gate are
// optional: onCreated is the SaaS org-created hook (nil = no billing), gate is
// nil to disable org-cap enforcement (SaaS / Licensed editions).
func MustNewCreateOrganizationHandler(orgs orgWriter, projects globalProjectWriter, members creatorMemberWriter, txor service.Transactor, accounts creatorAccountReader, onCreated OrgCreatedHook, gate OrgLimitGate) CreateOrganizationHandler {
	if orgs == nil || projects == nil || members == nil || accounts == nil {
		panic("CreateOrganizationHandler requires non-nil write ports and creatorAccountReader")
	}

	if txor == nil {
		panic("CreateOrganizationHandler requires a non-nil service.Transactor")
	}

	return CreateOrganizationHandler{orgs: orgs, projects: projects, members: members, txor: txor, accounts: accounts, onCreated: onCreated, gate: gate}
}

// Handle creates the organization with its global project and the creator's
// active membership in one atomic operation.
//
// Validation errors are returned as errs.InvalidError. Store errors are
// translated through translateErr before being returned.
func (h CreateOrganizationHandler) Handle(ctx context.Context, cmd CreateOrganizationCommand) (CreateOrganizationResult, error) {
	if cmd.CreatorAccountID == uuid.Nil {
		return CreateOrganizationResult{}, errs.InvalidError{
			Field:  "creator_account_id",
			Reason: reasonNilUUID,
		}
	}

	orgID := uuid.New()
	globalProjectID := uuid.New()
	memberID := uuid.New()

	org, err := model.NewOrganization(orgID, cmd.Name)
	if err != nil {
		return CreateOrganizationResult{}, err
	}

	globalProject, err := model.NewProjectInOrg(globalProjectID, "default", orgID)
	if err != nil {
		return CreateOrganizationResult{}, err
	}

	creatorMember, err := model.NewActiveMember(memberID, cmd.CreatorAccountID)
	if err != nil {
		return CreateOrganizationResult{}, err
	}

	// The owner member inherits the account's identity so the dashboard and
	// routing see an email/name from day one. Best-effort: a missing account
	// fails later on the FK anyway.
	if account, accErr := h.accounts.ByID(ctx, cmd.CreatorAccountID); accErr == nil {
		creatorMember.Email = account.Email
		creatorMember.DisplayName = account.DisplayName
	}

	if err = h.provision(ctx, cmd.CreatorAccountID, org, globalProject, creatorMember); err != nil {
		return CreateOrganizationResult{}, translateErr(err, "organization")
	}

	return CreateOrganizationResult{
		OrgID:           orgID,
		GlobalProjectID: globalProjectID,
	}, nil
}

// provision performs the six org-creation writes in one transaction, composed
// here rather than in an OrganizationAtomicStore adapter — each store call joins
// the ambient tx through its context. The creator's admin authority comes from
// the org_members write, not the project membership.
func (h CreateOrganizationHandler) provision(ctx context.Context, creatorAccountID uuid.UUID, org model.Organization, globalProject model.Project, creatorMember model.Member) error {
	return h.txor.WithTx(ctx, func(ctx context.Context) error {
		if h.gate != nil {
			if err := h.gate.AllowNewOrg(ctx); err != nil {
				return err
			}
		}

		if err := h.orgs.Create(ctx, org); err != nil {
			return err
		}

		if err := h.orgs.AddMember(ctx, model.OrgMembership{OrgID: org.ID, AccountID: creatorAccountID, Role: model.OrgRoleAdmin}); err != nil {
			return err
		}

		if err := h.projects.CreateInOrg(ctx, globalProject); err != nil {
			return err
		}

		if err := h.members.CreateWithAccount(ctx, creatorMember); err != nil {
			return err
		}

		if err := h.projects.AddMember(ctx, repository.ProjectMembership{
			ProjectID: globalProject.ID,
			MemberID:  creatorMember.ID,
			Role:      model.RoleUnspecified,
			Domains:   []string{},
		}); err != nil {
			return err
		}

		// SaaS opens a billing trial here (inside the tx); community has no hook.
		if h.onCreated != nil {
			return h.onCreated.OnOrgCreated(ctx, org.ID, creatorAccountID)
		}

		return nil
	})
}
