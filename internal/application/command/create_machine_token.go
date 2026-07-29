// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// CreateMachineTokenCommand requests a new org-scoped machine token.
type CreateMachineTokenCommand struct {
	MemberID   uuid.UUID
	OrgID      uuid.UUID
	IsOrgAdmin bool
	Label      string
	ProjectIDs []uuid.UUID `exhaustruct:"optional"`
}

// CreateMachineTokenResult contains token metadata and its one-time secret.
type CreateMachineTokenResult struct {
	ID         uuid.UUID
	Label      string
	ProjectIDs []uuid.UUID `exhaustruct:"optional"`
	CreatedAt  time.Time
	ExpiresAt  time.Time
	Secret     string
}

// MintedMachineToken is the machine-token adapter result.
type MintedMachineToken struct {
	ID        uuid.UUID
	Secret    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type machineTokenMinter interface {
	MintMachineToken(ctx context.Context, orgID, memberID uuid.UUID, projectIDs []uuid.UUID, label string) (MintedMachineToken, error)
}

type machineTokenProjectReader interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Project, error)
}

// CreateMachineTokenHandler creates machine tokens for org administrators.
type CreateMachineTokenHandler struct {
	tokens   machineTokenMinter
	projects machineTokenProjectReader
}

// MustNewCreateMachineTokenHandler builds a CreateMachineTokenHandler.
func MustNewCreateMachineTokenHandler(tokens machineTokenMinter, projects machineTokenProjectReader) CreateMachineTokenHandler {
	if tokens == nil {
		panic("CreateMachineTokenHandler requires a non-nil machineTokenMinter")
	}

	if projects == nil {
		panic("CreateMachineTokenHandler requires a non-nil machineTokenProjectReader")
	}

	return CreateMachineTokenHandler{tokens: tokens, projects: projects}
}

// Handle validates and creates a machine token.
func (h CreateMachineTokenHandler) Handle(ctx context.Context, cmd CreateMachineTokenCommand) (CreateMachineTokenResult, error) {
	if !cmd.IsOrgAdmin {
		return CreateMachineTokenResult{}, errs.ForbiddenError{Action: "create a machine token"}
	}

	if cmd.OrgID == uuid.Nil {
		return CreateMachineTokenResult{}, errs.InvalidError{Field: fieldOrgID, Reason: reasonNoOrgResolved}
	}

	label := strings.TrimSpace(cmd.Label)
	if label == "" {
		return CreateMachineTokenResult{}, errs.InvalidError{Field: "label", Reason: reasonEmpty}
	}

	if err := h.verifyProjectsInOrg(ctx, cmd.OrgID, cmd.ProjectIDs); err != nil {
		return CreateMachineTokenResult{}, err
	}

	minted, err := h.tokens.MintMachineToken(ctx, cmd.OrgID, cmd.MemberID, cmd.ProjectIDs, label)
	if err != nil {
		return CreateMachineTokenResult{}, errs.InternalError{Err: fmt.Errorf("minting machine token: %w", err)}
	}

	return CreateMachineTokenResult{
		ID:         minted.ID,
		Label:      label,
		ProjectIDs: cmd.ProjectIDs,
		CreatedAt:  minted.CreatedAt,
		ExpiresAt:  minted.ExpiresAt,
		Secret:     minted.Secret,
	}, nil
}

func (h CreateMachineTokenHandler) verifyProjectsInOrg(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) error {
	for _, id := range ids {
		project, err := h.projects.ByID(ctx, id)
		if err != nil {
			return translateErr(err, "project")
		}

		if project.OrgID != orgID {
			return errs.ForbiddenError{Action: "scope a machine token to a project outside your organization"}
		}
	}

	return nil
}
