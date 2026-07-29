// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/repository"
)

// GetJoinCodeQuery reads the caller org's live join code (if any). OrgID comes
// from the authenticated caller (never a request field).
type GetJoinCodeQuery struct {
	OrgID uuid.UUID
}

// JoinCodeView is the org's current join-code surface for the dashboard. Active
// is false and Code empty when no live code exists (UI shows "no code yet").
type JoinCodeView struct {
	Code        string
	ProjectID   uuid.UUID
	ProjectName string
	Active      bool
}

// activeJoinCodeReader resolves the org's current live join code. ok is false
// (no error) when the org has no live code. *identity.JoinTokenStore satisfies it.
type activeJoinCodeReader interface {
	ActiveJoinToken(ctx context.Context, orgID uuid.UUID) (repository.JoinToken, bool, error)
}

// joinCodeProjectNamer resolves a project's display name for the response.
// *identity.ProjectStore satisfies it.
type joinCodeProjectNamer interface {
	ReadProjectName(ctx context.Context, id uuid.UUID) (string, error)
}

// GetJoinCodeHandler handles GetJoinCodeQuery.
type GetJoinCodeHandler struct {
	tokens   activeJoinCodeReader
	projects joinCodeProjectNamer
}

// MustNewGetJoinCodeHandler builds the handler. It panics on a nil
// dependency, per project convention.
func MustNewGetJoinCodeHandler(tokens activeJoinCodeReader, projects joinCodeProjectNamer) GetJoinCodeHandler {
	if tokens == nil || projects == nil {
		panic("GetJoinCodeHandler requires a non-nil reader and project namer")
	}

	return GetJoinCodeHandler{tokens: tokens, projects: projects}
}

// Handle returns the org's live join code, or an inactive view when none exists.
func (h GetJoinCodeHandler) Handle(ctx context.Context, q GetJoinCodeQuery) (JoinCodeView, error) {
	token, ok, err := h.tokens.ActiveJoinToken(ctx, q.OrgID)
	if err != nil {
		return JoinCodeView{}, translateReadError(err, "join_code")
	}

	if !ok {
		// No live code: inactive view so the UI can show "no code yet".
		return JoinCodeView{Code: "", ProjectID: uuid.Nil, ProjectName: "", Active: false}, nil
	}

	// Best-effort name: the code is still valid even if the name lookup fails.
	name, err := h.projects.ReadProjectName(ctx, token.ProjectID)
	if err != nil {
		name = ""
	}

	return JoinCodeView{
		Code:        token.Token,
		ProjectID:   token.ProjectID,
		ProjectName: name,
		Active:      true,
	}, nil
}
