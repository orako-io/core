// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
)

// requireConversationAccess authorizes a member-action RPC that targets a bare
// conversation_id (ResolveConversation, DismissConversation). It resolves the
// conversation's project and defers to requireProjectMember, so the caller must
// be a member of that project — or an unscoped org admin of its org. Without
// this, any authenticated caller could act on any conversation by UUID
// (cross-project and cross-org), since the interceptor only admin-gates and
// these RPCs carry no project_id.
func (s *Server) requireConversationAccess(ctx context.Context, conversationID uuid.UUID) error {
	conv, err := s.convScope.ConversationByID(ctx, conversationID)
	if err != nil {
		if errors.Is(err, adaptererr.ErrNotFound) {
			return connect.NewError(connect.CodeNotFound, errors.New("conversation not found"))
		}

		return connect.NewError(connect.CodeInternal, err)
	}

	return s.requireProjectMember(ctx, conv.ProjectID)
}

// requireProjectInCallerOrg authorizes an org-admin RPC that targets a project
// (AddMember, AssignRole): the interceptor already proved the caller is an
// admin of THEIR org, but the request's project_id is attacker-supplied, so the
// project must be confirmed to belong to the caller's org — else an admin of
// org X could act on org Y's project by UUID.
func (s *Server) requireProjectInCallerOrg(ctx context.Context, projectID uuid.UUID) error {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	project, err := s.projects.ByID(ctx, projectID)
	if err != nil {
		if errors.Is(err, adaptererr.ErrNotFound) {
			return connect.NewError(connect.CodeNotFound, errors.New("project not found"))
		}

		return connect.NewError(connect.CodeInternal, err)
	}

	if caller.OrgID == uuid.Nil || project.OrgID != caller.OrgID {
		return connect.NewError(connect.CodePermissionDenied, errors.New("project is not in the caller's organization"))
	}

	return nil
}

// requireMemberInCallerOrg authorizes an org-admin RPC that targets a member
// (RemoveMember, SetMemberActivation, SetMemberAvailability): the target member
// must share the caller's org. Same cross-tenant rationale as
// requireProjectInCallerOrg.
func (s *Server) requireMemberInCallerOrg(ctx context.Context, memberID uuid.UUID) error {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if caller.OrgID == uuid.Nil {
		return connect.NewError(connect.CodePermissionDenied, errors.New("caller has no organization"))
	}

	memberships, err := s.projects.ProjectsByMember(ctx, memberID)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}

	for _, m := range memberships {
		if m.OrgID == caller.OrgID {
			return nil
		}
	}

	return connect.NewError(connect.CodePermissionDenied, errors.New("member is not in the caller's organization"))
}
