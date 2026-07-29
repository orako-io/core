// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// DeleteConversationCommand hard-deletes a conversation and every dependent row
// (messages, candidates, provider_messages and its surface row via ON DELETE
// CASCADE; the derived KB entry explicitly) plus its platform thread (the
// Discord thread is deleted best-effort). Irreversible. Org-admin gated at the transport
// interceptor; OrgID additionally scopes the target to the caller's own
// organization so an admin of one org can never delete another org's data.
type DeleteConversationCommand struct {
	ConversationID uuid.UUID
	// OrgID is the caller's own organization (CallerIdentity.OrgID), never
	// taken from the request.
	OrgID uuid.UUID
}

// DeleteConversationResult is the (empty) result of DeleteConversation.
type DeleteConversationResult struct{}

// conversationDeleter is the narrow read+write port DeleteConversation needs.
// Satisfied by *conversation.Store.
type conversationDeleter interface {
	ConversationByID(ctx context.Context, id uuid.UUID) (model.Conversation, error)
	DeleteConversation(ctx context.Context, id uuid.UUID) error
}

// projectOrgReader resolves a project to its owning org, for the org-scope
// check. Satisfied by *identity.ProjectStore.
type projectOrgReader interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Project, error)
}

// surfaceRemover deletes the conversation's platform thread (best-effort,
// never blocks the delete). Satisfied by *application.SurfaceManager; nil
// disables the mirror deletion.
type surfaceRemover interface {
	DeleteSurface(ctx context.Context, projectID, conversationID uuid.UUID)
}

// DeleteConversationHandler handles DeleteConversationCommand.
type DeleteConversationHandler struct {
	conversations conversationDeleter
	projects      projectOrgReader
	surfaces      surfaceRemover
}

// MustNewDeleteConversationHandler builds a handler. It panics on a
// nil required dependency; surfaces may be nil (no platform-thread mirror).
func MustNewDeleteConversationHandler(conversations conversationDeleter, projects projectOrgReader, surfaces surfaceRemover) DeleteConversationHandler {
	if conversations == nil {
		panic("DeleteConversationHandler requires a non-nil conversationDeleter")
	}

	if projects == nil {
		panic("DeleteConversationHandler requires a non-nil projectOrgReader")
	}

	return DeleteConversationHandler{conversations: conversations, projects: projects, surfaces: surfaces}
}

// Handle resolves the conversation's project, proves it belongs to the caller's
// org, then hard-deletes the conversation and its dependents.
func (h DeleteConversationHandler) Handle(ctx context.Context, cmd DeleteConversationCommand) (DeleteConversationResult, error) {
	conv, err := h.conversations.ConversationByID(ctx, cmd.ConversationID)
	if err != nil {
		return DeleteConversationResult{}, translateErr(err, "conversation")
	}

	project, err := h.projects.ByID(ctx, conv.ProjectID)
	if err != nil {
		return DeleteConversationResult{}, translateErr(err, "project")
	}

	if err := verifyProjectInOrg(project, cmd.OrgID); err != nil {
		return DeleteConversationResult{}, err
	}

	// Mirror the delete onto the platform: the Discord thread goes away with
	// the conversation. BEFORE the row delete (the surface row cascades with
	// it), and best-effort — a platform failure never blocks the delete.
	if h.surfaces != nil {
		h.surfaces.DeleteSurface(ctx, conv.ProjectID, cmd.ConversationID)
	}

	if err := h.conversations.DeleteConversation(ctx, cmd.ConversationID); err != nil {
		return DeleteConversationResult{}, translateErr(err, "conversation")
	}

	return DeleteConversationResult{}, nil
}
