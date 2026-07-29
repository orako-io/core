// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// seedConvInProject creates an open conversation in a project owned by orgID,
// wiring both fakes so the handler's project→org lookup resolves.
func seedConvInProject(convRepo *fakeConversationRepository, projRepo *fakeProjectRepository, orgID uuid.UUID) model.Conversation {
	project, _ := model.NewProjectInOrg(uuid.New(), "P", orgID)
	projRepo.projects[project.ID] = project

	conv := openConv(convRepo, project.ID, uuid.New(), uuid.New(), "q")

	return conv
}

// TestDeleteConversation_RemovesConversation proves an org admin's delete of a
// conversation in their own org removes it from the store.
func TestDeleteConversation_RemovesConversation(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	convRepo := newFakeConvRepo()
	projRepo := newFakeProjectRepo()
	conv := seedConvInProject(convRepo, projRepo, orgID)

	h := MustNewDeleteConversationHandler(convRepo, projRepo, nil)

	if _, err := h.Handle(t.Context(), DeleteConversationCommand{ConversationID: conv.ID, OrgID: orgID}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if _, ok := convRepo.conversations[conv.ID]; ok {
		t.Error("conversation still present after delete")
	}

	if !convRepo.deletedConvs[conv.ID] {
		t.Error("DeleteConversation was not called on the store")
	}
}

// TestDeleteConversation_CrossTenantRejected proves an org admin cannot delete a
// conversation in a project owned by a different organization, and it survives.
func TestDeleteConversation_CrossTenantRejected(t *testing.T) {
	t.Parallel()

	ownerOrg := uuid.New()
	callerOrg := uuid.New()
	convRepo := newFakeConvRepo()
	projRepo := newFakeProjectRepo()
	conv := seedConvInProject(convRepo, projRepo, ownerOrg)

	h := MustNewDeleteConversationHandler(convRepo, projRepo, nil)

	_, err := h.Handle(t.Context(), DeleteConversationCommand{ConversationID: conv.ID, OrgID: callerOrg})

	var forbidden errs.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("Handle(cross-tenant): got %v, want errs.ForbiddenError", err)
	}

	if _, ok := convRepo.conversations[conv.ID]; !ok {
		t.Error("conversation must survive a cross-tenant delete attempt")
	}
}

// TestDeleteConversation_NotFound proves an absent conversation surfaces
// NotFoundError and deletes nothing.
func TestDeleteConversation_NotFound(t *testing.T) {
	t.Parallel()

	h := MustNewDeleteConversationHandler(newFakeConvRepo(), newFakeProjectRepo(), nil)

	_, err := h.Handle(t.Context(), DeleteConversationCommand{ConversationID: uuid.New(), OrgID: uuid.New()})

	var notFound errs.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Handle(absent conversation): got %v, want errs.NotFoundError", err)
	}
}
