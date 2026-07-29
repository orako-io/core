// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/query"
)

// TestGetConversationCrossOrgAdminDenied is the regression test for the R1
// multi-tenant IDOR: an org admin must not be able to read a conversation that
// belongs to a DIFFERENT org just by supplying its UUID. GetConversation now
// gates on requireConversationAccess (project→org scope) before dispatch, so a
// foreign-org admin is rejected while a member of the conversation's project
// still gets through.
func TestGetConversationCrossOrgAdminDenied(t *testing.T) {
	t.Parallel()

	orgA := uuid.New()
	orgB := uuid.New()
	convProject := uuid.New()   // the conversation's project, owned by orgB
	callerProject := uuid.New() // the foreign admin's own project, in orgA

	srv := &Server{
		convScope: &fakeConvScope{projectID: convProject},
		projects: &fakeProjectResolver{byID: map[uuid.UUID]model.Project{
			convProject:   {ID: convProject, OrgID: orgB},
			callerProject: {ID: callerProject, OrgID: orgA},
		}},
		getConversation: &fakeGetConversation{view: query.ConversationView{}},
	}

	req := connect.NewRequest(&orakov1.GetConversationRequest{ConversationId: uuid.New().String()})

	// An admin of orgA — a different org than the conversation's orgB — is denied.
	foreignAdmin := withCaller(context.Background(), CallerIdentity{
		MemberID:   uuid.New(),
		ProjectID:  callerProject,
		ProjectIDs: []uuid.UUID{callerProject},
		OrgID:      orgA,
		IsOrgAdmin: true,
	})

	if _, err := srv.GetConversation(foreignAdmin, req); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("cross-org admin read: want PermissionDenied, got %v", err)
	}

	// A member of the conversation's own project passes the scope gate.
	member := withCaller(context.Background(), CallerIdentity{
		MemberID:   uuid.New(),
		ProjectID:  convProject,
		ProjectIDs: []uuid.UUID{convProject},
		OrgID:      orgB,
	})

	if _, err := srv.GetConversation(member, req); err != nil {
		t.Fatalf("project member read: want nil, got %v", err)
	}
}
