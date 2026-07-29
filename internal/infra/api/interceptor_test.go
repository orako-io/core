// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/gen/orako/v1/orakov1connect"
	"github.com/orako-io/core/internal/application/command"
)

// ── Token parsing ────────────────────────────────────────────────────────────

func TestParseDevToken_Valid(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	projectID := uuid.New()

	token := "Bearer " + memberID.String() + ":" + projectID.String() + ":lead"

	id, err := parseDevToken(token)
	if err != nil {
		t.Fatalf("parseDevToken returned error: %v", err)
	}

	if id.MemberID != memberID {
		t.Errorf("MemberID = %v, want %v", id.MemberID, memberID)
	}

	if id.ProjectID != projectID {
		t.Errorf("ProjectID = %v, want %v", id.ProjectID, projectID)
	}

	if id.Role.String() != "lead" {
		t.Errorf("Role = %v, want lead", id.Role)
	}
}

func TestParseDevToken_Missing(t *testing.T) {
	t.Parallel()

	_, err := parseDevToken("")
	if err == nil {
		t.Fatal("expected error for missing token, got nil")
	}
}

func TestParseDevToken_MalformedNotBearer(t *testing.T) {
	t.Parallel()

	_, err := parseDevToken("Basic abc123")
	if err == nil {
		t.Fatal("expected error for non-Bearer token, got nil")
	}
}

func TestParseDevToken_BadMemberID(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	token := "Bearer not-a-uuid:" + projectID.String() + ":dev"
	_, err := parseDevToken(token)

	if err == nil {
		t.Fatal("expected error for bad member_id, got nil")
	}
}

func TestParseDevToken_UnknownRole(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	projectID := uuid.New()
	token := "Bearer " + memberID.String() + ":" + projectID.String() + ":superuser"

	_, err := parseDevToken(token)
	if err == nil {
		t.Fatal("expected error for unknown role, got nil")
	}
}

func TestParseDevToken_OrgAdminFlag(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	projectID := uuid.New()

	// A non-admin project role plus the :orgadmin flag yields IsOrgAdmin.
	id, err := parseDevToken("Bearer " + memberID.String() + ":" + projectID.String() + ":specialist:orgadmin")
	if err != nil {
		t.Fatalf("parseDevToken: %v", err)
	}

	if !id.IsOrgAdmin {
		t.Error("IsOrgAdmin = false, want true for the :orgadmin flag")
	}
}

func TestParseDevToken_AdminRoleImpliesOrgAdmin(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	projectID := uuid.New()

	// Backward-compat: a 3-segment token with role "admin" is treated as org admin.
	id, err := parseDevToken("Bearer " + memberID.String() + ":" + projectID.String() + ":admin")
	if err != nil {
		t.Fatalf("parseDevToken: %v", err)
	}

	if !id.IsOrgAdmin {
		t.Error("IsOrgAdmin = false, want true for the legacy admin role")
	}
}

func TestParseDevToken_NonAdminRoleIsNotOrgAdmin(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	projectID := uuid.New()

	id, err := parseDevToken("Bearer " + memberID.String() + ":" + projectID.String() + ":lead")
	if err != nil {
		t.Fatalf("parseDevToken: %v", err)
	}

	if id.IsOrgAdmin {
		t.Error("IsOrgAdmin = true, want false for a non-admin role without the flag")
	}
}

func TestParseDevToken_BadOrgAdminFlag(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	projectID := uuid.New()

	_, err := parseDevToken("Bearer " + memberID.String() + ":" + projectID.String() + ":dev:bogus")
	if err == nil {
		t.Fatal("expected error for an invalid 4th token segment, got nil")
	}
}

// ── Interceptor: unauthenticated ─────────────────────────────────────────────

func TestInterceptor_NoToken_Unauthenticated(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	// No Authorization header → must fail with CodeUnauthenticated.
	req := connect.NewRequest(&orakov1.HeartbeatRequest{})

	_, err := client.Heartbeat(t.Context(), req)
	assertConnectCode(t, err, connect.CodeUnauthenticated)
}

// ── Interceptor: identity injection ──────────────────────────────────────────

// capturingHeartbeat records the CallerIdentity it receives from context.
type capturingHeartbeat struct {
	captured CallerIdentity
}

func (c *capturingHeartbeat) Handle(ctx context.Context, _ command.HeartbeatCommand) error {
	id, ok := CallerFromContext(ctx)
	if ok {
		c.captured = id
	}

	return nil
}

func TestInterceptor_IdentityInjectedInContext(t *testing.T) {
	t.Parallel()

	capHB := &capturingHeartbeat{}
	svc := zeroServer()
	svc.heartbeat = capHB

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	memberID := uuid.New()
	projectID := uuid.New()

	req := connect.NewRequest(&orakov1.HeartbeatRequest{})
	setAuthCustom(req, memberID.String(), projectID.String(), "dev")

	_, err := client.Heartbeat(t.Context(), req)
	if err != nil {
		t.Fatalf("Heartbeat returned error: %v", err)
	}

	if capHB.captured.MemberID != memberID {
		t.Errorf("captured MemberID = %v, want %v", capHB.captured.MemberID, memberID)
	}

	if capHB.captured.ProjectID != projectID {
		t.Errorf("captured ProjectID = %v, want %v", capHB.captured.ProjectID, projectID)
	}
}

// TestInterceptor_IdentityFromTokenNotRequestField verifies that the identity
// in context comes from the token, not from any request field.
// The test uses Heartbeat (no identity fields in the request proto) and verifies
// the context carries the token's memberID. A spoofed field in the request
// cannot override the token because the proto has no such field — invariant (e).
func TestInterceptor_IdentityFromTokenNotRequestField(t *testing.T) {
	t.Parallel()

	realMemberID := uuid.New()
	realProjectID := uuid.New()

	capHB := &capturingHeartbeat{}
	svc := zeroServer()
	svc.heartbeat = capHB

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.HeartbeatRequest{})
	setAuthCustom(req, realMemberID.String(), realProjectID.String(), "dev")

	_, err := client.Heartbeat(t.Context(), req)
	if err != nil {
		t.Fatalf("Heartbeat returned error: %v", err)
	}

	// Identity must come from the token.
	if capHB.captured.MemberID != realMemberID {
		t.Errorf("identity from context: MemberID = %v, want %v (from token, not request fields)", capHB.captured.MemberID, realMemberID)
	}
}

// ── Interceptor: admin gating ─────────────────────────────────────────────────

// TestInterceptor_DismissConversation_NotAdminGated proves dismissing a dead
// thread is normal member action, not an admin procedure: a non-admin must not
// be blocked by the admin gate. Guards the design decision that only delete
// (not dismiss) is org-admin gated.
func TestInterceptor_DismissConversation_NotAdminGated(t *testing.T) {
	t.Parallel()

	if isAdminProcedure(orakov1connect.OrakoServiceDismissConversationProcedure) {
		t.Error("DismissConversation must not be admin-gated; dismissing an unanswered conversation is normal member action")
	}
}

// TestInterceptor_ListMembers_NotAdminGated proves any org member may read the
// team roster: seeing who is on the team (to route a question to them) is normal
// member action, not an admin privilege. Guards the fix for non-admins seeing an
// empty team. Roster mutations and single-member detail stay admin-gated.
func TestInterceptor_ListMembers_NotAdminGated(t *testing.T) {
	t.Parallel()

	if isAdminProcedure(orakov1connect.OrakoServiceListMembersProcedure) {
		t.Error("ListMembers must not be admin-gated; every member may see the team roster")
	}

	if !isAdminProcedure(orakov1connect.OrakoServiceGetOrgMemberProcedure) {
		t.Error("GetOrgMember must stay admin-gated; single-member detail is an admin read")
	}
}

// TestInterceptor_DeleteConversation_AdminGated proves deleting a conversation
// is an admin procedure: destroying data requires org-admin authority.
func TestInterceptor_DeleteConversation_AdminGated(t *testing.T) {
	t.Parallel()

	if !isAdminProcedure(orakov1connect.OrakoServiceDeleteConversationProcedure) {
		t.Error("DeleteConversation must be admin-gated; a hard delete is a destructive admin action")
	}
}

// TestInterceptor_DeleteOrganization_AdminGated proves deleting an organization
// is an admin procedure: destroying an entire org's data requires org-admin
// authority.
func TestInterceptor_DeleteOrganization_AdminGated(t *testing.T) {
	t.Parallel()

	if !isAdminProcedure(orakov1connect.OrakoServiceDeleteOrganizationProcedure) {
		t.Error("DeleteOrganization must be admin-gated; deleting an org is the most destructive admin action")
	}
}

func TestInterceptor_NonAdmin_AddMember_PermissionDenied(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.AddMemberRequest{
		ProjectId: testProjectID.String(),
		Email:     "bob@example.com",
		Domains:   []string{"backend"},
	})
	setAuth(req, "dev") // dev is not lead/admin

	_, err := client.AddMember(t.Context(), req)
	assertConnectCode(t, err, connect.CodePermissionDenied)
}

func TestInterceptor_Specialist_CreateProject_PermissionDenied(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.CreateProjectRequest{Name: "my project"})
	setAuth(req, "specialist") // responder is not lead/admin

	_, err := client.CreateProject(t.Context(), req)
	assertConnectCode(t, err, connect.CodePermissionDenied)
}

// A former project lead is NOT an org admin, so it is now denied on admin RPCs:
// admin authority comes solely from org_members, not the project role.
func TestInterceptor_Lead_AddMember_DeniedNow(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.AddMemberRequest{
		ProjectId: testProjectID.String(),
		Email:     "charlie@example.com",
		Domains:   []string{"backend"},
	})
	setAuth(req, "lead") // lead is not an org admin

	_, err := client.AddMember(t.Context(), req)
	assertConnectCode(t, err, connect.CodePermissionDenied)
}

// An org admin (dev token with the :orgadmin flag) passes the admin gate even
// with a non-admin project role.
func TestInterceptor_OrgAdmin_AddMember_Allowed(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.AddMemberRequest{
		ProjectId: testProjectID.String(),
		Email:     "charlie@example.com",
		Domains:   []string{"backend"},
	})
	setAuthOrgAdmin(req, "specialist") // responder project role, but org admin

	_, err := client.AddMember(t.Context(), req)
	if err != nil {
		t.Fatalf("AddMember as org admin returned error: %v", err)
	}
}

func TestInterceptor_Admin_AssignRole_Allowed(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.AssignRoleRequest{
		ProjectId: testProjectID.String(),
		MemberId:  testMemberID.String(),
		Domains:   []string{"backend"},
	})
	setAuthOrgAdmin(req, "admin")

	_, err := client.AssignRole(t.Context(), req)
	if err != nil {
		t.Fatalf("AssignRole with admin role returned error: %v", err)
	}
}

func TestInterceptor_Admin_RemoveMember_Allowed(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.RemoveMemberRequest{
		ProjectId: testProjectID.String(),
		MemberId:  testMemberID.String(),
		PurgePii:  false,
	})
	setAuthOrgAdmin(req, "admin")

	_, err := client.RemoveMember(t.Context(), req)
	if err != nil {
		t.Fatalf("RemoveMember with admin role returned error: %v", err)
	}
}

// ── Interceptor: project management (phase 3) ────────────────────────────────

// projectManagementCall exercises one of the five new project-management RPCs
// against client, returning its error (nil on success).
type projectManagementCall struct {
	name string
	call func(ctx context.Context, client orakov1connect.OrakoServiceClient) error
}

// projectManagementCalls is the shared table of the five new project-
// management RPCs, reused by both the non-admin-denied and org-admin-allowed
// tests below.
func projectManagementCalls(role string) []projectManagementCall {
	return []projectManagementCall{
		{"RenameProject", func(ctx context.Context, client orakov1connect.OrakoServiceClient) error {
			req := connect.NewRequest(&orakov1.RenameProjectRequest{ProjectId: testProjectID.String(), Name: "New Name"})
			setAuth(req, role)
			_, err := client.RenameProject(ctx, req)
			return err
		}},
		{"SetProjectArchived", func(ctx context.Context, client orakov1connect.OrakoServiceClient) error {
			req := connect.NewRequest(&orakov1.SetProjectArchivedRequest{ProjectId: testProjectID.String(), Archived: true})
			setAuth(req, role)
			_, err := client.SetProjectArchived(ctx, req)
			return err
		}},
		{"DeleteProject", func(ctx context.Context, client orakov1connect.OrakoServiceClient) error {
			req := connect.NewRequest(&orakov1.DeleteProjectRequest{ProjectId: testProjectID.String()})
			setAuth(req, role)
			_, err := client.DeleteProject(ctx, req)
			return err
		}},
		{"ListProjectsDetailed", func(ctx context.Context, client orakov1connect.OrakoServiceClient) error {
			req := connect.NewRequest(&orakov1.ListProjectsDetailedRequest{})
			setAuth(req, role)
			_, err := client.ListProjectsDetailed(ctx, req)
			return err
		}},
	}
}

// TestInterceptor_ProjectManagement_NonAdminDenied proves every new project-
// management RPC (RenameProject, SetProjectArchived, DeleteProject,
// ListProjectsDetailed) rejects a non-org-admin caller.
func TestInterceptor_ProjectManagement_NonAdminDenied(t *testing.T) {
	t.Parallel()

	for _, tc := range projectManagementCalls("dev") { // dev is not lead/admin
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, client := startTestServerWithAuth(zeroServer())
			defer srv.Close()

			assertConnectCode(t, tc.call(t.Context(), client), connect.CodePermissionDenied)
		})
	}
}

// TestInterceptor_ProjectManagement_OrgAdminAllowed proves an org admin passes
// the gate on every new project-management RPC (the handler itself is a
// no-op fake, so success here proves the interceptor let the request through).
func TestInterceptor_ProjectManagement_OrgAdminAllowed(t *testing.T) {
	t.Parallel()

	for _, tc := range projectManagementCalls("admin") {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, client := startTestServerWithAuth(zeroServer())
			defer srv.Close()

			if err := tc.call(t.Context(), client); err != nil {
				t.Fatalf("%s with admin role returned error: %v", tc.name, err)
			}
		})
	}
}
