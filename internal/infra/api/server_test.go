// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/errs"
)

// testMemberID / testProjectID / testOrgID are fixed UUIDs re-used across all
// tests. testProjectID belongs to testOrgID, and testMemberID is a member of
// testProjectID — see seededProjectResolver, the default zeroServer resolver.
var (
	testMemberID  = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	testProjectID = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	testOrgID     = uuid.MustParse("00000000-0000-0000-0000-000000000003")
)

// seededProjectResolver is the default org-scope resolver: testProjectID is in
// testOrgID and testMemberID is a member of it, so an org-admin caller scoped to
// testOrgID (setAuthOrgAdmin) passes requireProjectInCallerOrg /
// requireMemberInCallerOrg. Unseeded ids resolve to not-found / no-membership,
// keeping the deny paths closed.
func seededProjectResolver() *fakeProjectResolver {
	project, _ := model.NewProjectInOrg(testProjectID, "test", testOrgID)

	return &fakeProjectResolver{
		byID: map[uuid.UUID]model.Project{testProjectID: project},
		byMember: map[uuid.UUID][]repository.ProjectWithRole{
			testMemberID: {{ID: testProjectID, Name: "test", Role: model.RoleUnspecified, OrgID: testOrgID}},
		},
	}
}

// assertConnectCode fails the test if err is not a *connect.Error with the
// expected code.
func assertConnectCode(t *testing.T, err error, want connect.Code) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected connect error with code %v, got nil", want)
	}

	var connErr *connect.Error

	if !errors.As(err, &connErr) {
		t.Fatalf("expected *connect.Error, got %T: %v", err, err)
	}

	if connErr.Code() != want {
		t.Errorf("code = %v, want %v", connErr.Code(), want)
	}
}

// TestAddMember_ForeignOrgDenied proves an org admin cannot add a member to a
// project in a different org (H3 fix): the project resolves to another org.
func TestAddMember_ForeignOrgDenied(t *testing.T) {
	t.Parallel()

	foreign, _ := model.NewProjectInOrg(uuid.New(), "foreign", uuid.New()) // different org
	svc := zeroServer()
	svc.projects = &fakeProjectResolver{byID: map[uuid.UUID]model.Project{foreign.ID: foreign}}

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.AddMemberRequest{
		ProjectId: foreign.ID.String(),
		Email:     "x@example.com",
		Domains:   []string{"backend"},
	})
	setAuthOrgAdmin(req, "admin") // caller's OrgID = testOrgID, not the project's org

	_, err := client.AddMember(t.Context(), req)
	assertConnectCode(t, err, connect.CodePermissionDenied)
}

// TestDisconnectProvider_ForeignOrgDenied proves an org admin cannot disconnect
// a provider on a project in another org (S2 fix).
func TestDisconnectProvider_ForeignOrgDenied(t *testing.T) {
	t.Parallel()

	foreign, _ := model.NewProjectInOrg(uuid.New(), "foreign", uuid.New())
	svc := zeroServer()
	svc.projects = &fakeProjectResolver{byID: map[uuid.UUID]model.Project{foreign.ID: foreign}}

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.DisconnectProviderRequest{ProjectId: foreign.ID.String(), Kind: "slack"})
	setAuthOrgAdmin(req, "admin")

	_, err := client.DisconnectProvider(t.Context(), req)
	assertConnectCode(t, err, connect.CodePermissionDenied)
}

// TestConfigureProvider_ForeignOrgDenied proves an org admin cannot configure a
// provider on a project in another org (S3 fix).
func TestConfigureProvider_ForeignOrgDenied(t *testing.T) {
	t.Parallel()

	foreign, _ := model.NewProjectInOrg(uuid.New(), "foreign", uuid.New())
	svc := zeroServer()
	svc.projects = &fakeProjectResolver{byID: map[uuid.UUID]model.Project{foreign.ID: foreign}}

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.ConfigureProviderRequest{
		ProjectId:   foreign.ID.String(),
		Kind:        "slack",
		Credentials: map[string]string{"bot_token": "xoxb-x"},
	})
	setAuthOrgAdmin(req, "admin")

	_, err := client.ConfigureProvider(t.Context(), req)
	assertConnectCode(t, err, connect.CodePermissionDenied)
}

// TestRemoveMember_ForeignOrgDenied proves an org admin cannot remove a member
// who belongs to a different org (H3 fix).
func TestRemoveMember_ForeignOrgDenied(t *testing.T) {
	t.Parallel()

	foreignMember := uuid.New()
	svc := zeroServer()
	svc.projects = &fakeProjectResolver{byMember: map[uuid.UUID][]repository.ProjectWithRole{
		foreignMember: {{ID: uuid.New(), OrgID: uuid.New()}}, // member in a different org
	}}

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.RemoveMemberRequest{
		ProjectId: testProjectID.String(),
		MemberId:  foreignMember.String(),
	})
	setAuthOrgAdmin(req, "admin")

	_, err := client.RemoveMember(t.Context(), req)
	assertConnectCode(t, err, connect.CodePermissionDenied)
}

// ── ListExperts ────────────────────────────────────────────────────────────

func TestListExperts_HappyPath(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	svc.listExperts = &fakeListExperts{
		experts: []query.Expert{
			{
				MemberID:    testMemberID,
				DisplayName: "Alice",
				Domains:     []string{"backend", "cto"},
				Online:      true,
			},
		},
	}

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.ListExpertsRequest{
		ProjectId: testProjectID.String(),
	})
	setAuth(req, "dev")

	resp, err := client.ListExperts(t.Context(), req)
	if err != nil {
		t.Fatalf("ListExperts returned error: %v", err)
	}

	if len(resp.Msg.GetExperts()) != 1 {
		t.Fatalf("experts len = %d, want 1", len(resp.Msg.GetExperts()))
	}

	// Domains (expertise tags) are surfaced for agent routing.
	if got := resp.Msg.GetExperts()[0].GetDomains(); len(got) != 2 || got[0] != "backend" {
		t.Errorf("Domains = %v, want [backend cto]", got)
	}
}

// ── ListMembers (pending visibility) ────────────────────────────────────────

// capturingListMembers records the last ListMembersQuery it received so a test
// can assert whether pending members were requested.
type capturingListMembers struct{ last query.ListMembersQuery }

func (f *capturingListMembers) Handle(_ context.Context, q query.ListMembersQuery) ([]query.OrgMemberView, error) {
	f.last = q
	return nil, nil
}

// TestListMembers_PendingOnlyForAdmin proves the server asks for pending members
// ONLY when the caller is an org admin: a non-admin caller must get the roster
// with IncludePending=false, so no pending self-join member can ever reach them.
func TestListMembers_PendingOnlyForAdmin(t *testing.T) {
	t.Parallel()

	t.Run("non-admin: pending excluded", func(t *testing.T) {
		t.Parallel()

		svc := zeroServer()
		capFake := &capturingListMembers{}
		svc.listMembers = capFake

		srv, client := startTestServerWithAuth(svc)
		defer srv.Close()

		req := connect.NewRequest(&orakov1.ListMembersRequest{})
		setAuth(req, "specialist") // not org admin

		if _, err := client.ListMembers(t.Context(), req); err != nil {
			t.Fatalf("ListMembers returned error: %v", err)
		}

		if capFake.last.IncludePending {
			t.Error("IncludePending = true for a non-admin caller; pending must be hidden")
		}
	})

	t.Run("admin: pending included", func(t *testing.T) {
		t.Parallel()

		svc := zeroServer()
		capFake := &capturingListMembers{}
		svc.listMembers = capFake

		srv, client := startTestServerWithAuth(svc)
		defer srv.Close()

		req := connect.NewRequest(&orakov1.ListMembersRequest{})
		setAuthOrgAdmin(req, "admin")

		if _, err := client.ListMembers(t.Context(), req); err != nil {
			t.Fatalf("ListMembers returned error: %v", err)
		}

		if !capFake.last.IncludePending {
			t.Error("IncludePending = false for an org-admin caller; pending must be included")
		}
	})
}

// ── Ask ─────────────────────────────────────────────────────────────

func TestAsk_HappyPath(t *testing.T) {
	t.Parallel()

	convID := uuid.New()
	svc := zeroServer()
	svc.ask = &fakeAsk{result: command.AskResult{ConversationID: convID}}

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	targetID := uuid.New()

	req := connect.NewRequest(&orakov1.AskRequest{
		ProjectId: testProjectID.String(),
		MemberId:  targetID.String(),
		Question:  "what is orako?",
	})
	setAuth(req, "dev")

	resp, err := client.Ask(t.Context(), req)
	if err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}

	if resp.Msg.GetConversationId() != convID.String() {
		t.Errorf("conversation_id = %q, want %q", resp.Msg.GetConversationId(), convID.String())
	}
}

func TestAsk_InvalidSpecialistID(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.AskRequest{
		ProjectId: testProjectID.String(),
		MemberId:  "bad-uuid",
		Question:  "hello",
	})
	setAuth(req, "dev")

	_, err := client.Ask(t.Context(), req)
	assertConnectCode(t, err, connect.CodeInvalidArgument)
}

// ── GetConversation ───────────────────────────────────────────────────────────

func TestGetConversation_HappyPath(t *testing.T) {
	t.Parallel()

	convID := uuid.New()
	svc := zeroServer()
	svc.getConversation = &fakeGetConversation{
		view: query.ConversationView{
			ID:     convID,
			Status: model.ConversationStatusOpen,
		},
	}

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.GetConversationRequest{
		ConversationId: convID.String(),
	})
	setAuth(req, "dev")

	resp, err := client.GetConversation(t.Context(), req)
	if err != nil {
		t.Fatalf("GetConversation returned error: %v", err)
	}

	if resp.Msg.GetConversationId() != convID.String() {
		t.Errorf("conversation_id = %q, want %q", resp.Msg.GetConversationId(), convID.String())
	}

	if resp.Msg.GetStatus() != "open" {
		t.Errorf("status = %q, want %q", resp.Msg.GetStatus(), "open")
	}
}

// ── FollowUp ──────────────────────────────────────────────────────────────────

func TestFollowUp_HappyPath(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.FollowUpRequest{
		ConversationId: uuid.New().String(),
		Body:           "follow up message",
	})
	setAuth(req, "dev")

	_, err := client.FollowUp(t.Context(), req)
	if err != nil {
		t.Fatalf("FollowUp returned error: %v", err)
	}
}

// ── ResolveConversation ─────────────────────────────────────────────────────────

func TestResolveConversation_HappyPath(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	svc.resolveConversation = &fakeResolveConversation{}

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.ResolveConversationRequest{
		ConversationId: uuid.New().String(),
		Resolution:     "here is the answer",
	})
	setAuth(req, "dev")

	if _, err := client.ResolveConversation(t.Context(), req); err != nil {
		t.Fatalf("ResolveConversation returned error: %v", err)
	}
}

// TestResolveConversation_ContestedPassthrough proves the RPC handler reads
// request.contested / request.contested_set (phase 1 proto fields) into the
// command unchanged.
func TestResolveConversation_ContestedPassthrough(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	fake := &fakeResolveConversation{}
	svc.resolveConversation = fake

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.ResolveConversationRequest{
		ConversationId: uuid.New().String(),
		Resolution:     "here is the answer",
		Contested:      true,
		ContestedSet:   true,
	})
	setAuth(req, "dev")

	if _, err := client.ResolveConversation(t.Context(), req); err != nil {
		t.Fatalf("ResolveConversation returned error: %v", err)
	}

	if !fake.gotCmd.Contested {
		t.Error("command.Contested = false, want true")
	}

	if !fake.gotCmd.ContestedSet {
		t.Error("command.ContestedSet = false, want true")
	}
}

// TestDismissConversation_ForeignProjectDenied proves the scope guard rejects a
// caller who is not a member of the conversation's project (H2 fix): the
// conversation resolves to a project the dev caller does not belong to.
func TestDismissConversation_ForeignProjectDenied(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	svc.dismissConversation = &fakeDismissConversation{}
	svc.convScope = &fakeConvScope{projectID: uuid.New()} // not testProjectID → not a member

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.DismissConversationRequest{ConversationId: uuid.New().String()})
	setAuth(req, "dev")

	_, err := client.DismissConversation(t.Context(), req)
	assertConnectCode(t, err, connect.CodePermissionDenied)
}

// ── Heartbeat ─────────────────────────────────────────────────────────────────

func TestHeartbeat_HappyPath(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.HeartbeatRequest{})
	setAuth(req, "dev")

	_, err := client.Heartbeat(t.Context(), req)
	if err != nil {
		t.Fatalf("Heartbeat returned error: %v", err)
	}
}

// ── Admin RPCs ────────────────────────────────────────────────────────────────

func TestCreateProject_HappyPath(t *testing.T) {
	t.Parallel()

	newProjectID := uuid.New()
	svc := zeroServer()
	svc.createProject = &fakeCreateProject{
		result: command.CreateProjectResult{ProjectID: newProjectID},
	}

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.CreateProjectRequest{
		Name: "test project",
	})
	setAuthOrgAdmin(req, "admin")

	resp, err := client.CreateProject(t.Context(), req)
	if err != nil {
		t.Fatalf("CreateProject returned error: %v", err)
	}

	if resp.Msg.GetProjectId() != newProjectID.String() {
		t.Errorf("project_id = %q, want %q", resp.Msg.GetProjectId(), newProjectID.String())
	}
}

func TestAddMember_HappyPath(t *testing.T) {
	t.Parallel()

	newMemberID := uuid.New()
	svc := zeroServer()
	addMember := &fakeAddMember{
		result: command.AddMemberResult{MemberID: newMemberID},
	}
	svc.addMember = addMember

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.AddMemberRequest{
		ProjectId: testProjectID.String(),
		Email:     "alice@example.com",
		Domains:   []string{"backend", "cto"},
	})
	setAuthOrgAdmin(req, "admin")

	resp, err := client.AddMember(t.Context(), req)
	if err != nil {
		t.Fatalf("AddMember returned error: %v", err)
	}

	if resp.Msg.GetMemberId() != newMemberID.String() {
		t.Errorf("member_id = %q, want %q", resp.Msg.GetMemberId(), newMemberID.String())
	}

	// Domains flow request → command.
	if got := addMember.lastCmd.Domains; len(got) != 2 || got[0] != "backend" || got[1] != "cto" {
		t.Errorf("AddMemberCommand.Domains = %v, want [backend cto]", got)
	}
}

func TestAssignRole_HappyPath(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	assignRole := &fakeAssignRole{}
	svc.assignRole = assignRole
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.AssignRoleRequest{
		ProjectId: testProjectID.String(),
		MemberId:  testMemberID.String(),
		Domains:   []string{"frontend", "qa"},
	})
	setAuthOrgAdmin(req, "admin")

	_, err := client.AssignRole(t.Context(), req)
	if err != nil {
		t.Fatalf("AssignRole returned error: %v", err)
	}

	// Domains flow request → command (set-tags semantics).
	if got := assignRole.lastCmd.Domains; len(got) != 2 || got[0] != "frontend" || got[1] != "qa" {
		t.Errorf("AssignRoleCommand.Domains = %v, want [frontend qa]", got)
	}
}

func TestRemoveMember_HappyPath(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	remove := &fakeRemoveMember{}
	svc.removeMember = remove
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
		t.Fatalf("RemoveMember returned error: %v", err)
	}

	if remove.lastCmd.OrgID != testOrgID {
		t.Errorf("RemoveMemberCommand.OrgID = %s, want caller org %s", remove.lastCmd.OrgID, testOrgID)
	}
}

// ── ListProjects ──────────────────────────────────────────────────────────────

func TestListProjects_HappyPath(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	svc.listProjects = &fakeListProjects{
		summaries: []query.ProjectSummary{
			{ID: testProjectID, Name: "test-project", Role: model.RoleDev},
		},
	}

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.ListProjectsRequest{})
	setAuth(req, "dev")

	resp, err := client.ListProjects(t.Context(), req)
	if err != nil {
		t.Fatalf("ListProjects returned error: %v", err)
	}

	if len(resp.Msg.GetProjects()) != 1 {
		t.Fatalf("projects len = %d, want 1", len(resp.Msg.GetProjects()))
	}

	if resp.Msg.GetProjects()[0].GetId() != testProjectID.String() {
		t.Errorf("project id = %q, want %q", resp.Msg.GetProjects()[0].GetId(), testProjectID.String())
	}
}

func TestListProjects_Unauthenticated(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	// No Authorization header — auth interceptor returns Unauthenticated.
	req := connect.NewRequest(&orakov1.ListProjectsRequest{})

	_, err := client.ListProjects(t.Context(), req)
	assertConnectCode(t, err, connect.CodeUnauthenticated)
}

// ── ListConnectedChannels ─────────────────────────────────────────────────────

// TestListConnectedChannels_HappyPath proves the RPC populates both the
// legacy `channels` string list and the new `connected` field (phase 4,
// folded review #15 read path) from GetProviderAlertChannelsHandler, so the
// dashboard can prefill each provider's alert-channel input.
func TestListConnectedChannels_HappyPath(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	svc.listConnectedChannels = &fakeListConnectedChannels{channels: []string{"dashboard", "slack"}}
	svc.getProviderAlertChannels = &fakeGetProviderAlertChannels{
		channels: []service.ProviderAlertChannel{
			{Kind: "slack", AlertChannelIDs: []string{"C0SLACK"}},
			{Kind: "teams", AlertChannelIDs: nil},
		},
	}

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.ListConnectedChannelsRequest{})
	setAuth(req, "dev")

	resp, err := client.ListConnectedChannels(t.Context(), req)
	if err != nil {
		t.Fatalf("ListConnectedChannels returned error: %v", err)
	}

	if got := resp.Msg.GetChannels(); len(got) != 2 || got[0] != "dashboard" || got[1] != "slack" {
		t.Errorf("channels = %+v, want [dashboard slack]", got)
	}

	connected := resp.Msg.GetConnected()
	if len(connected) != 2 {
		t.Fatalf("connected len = %d, want 2", len(connected))
	}

	ch0 := connected[0].GetAlertChannelIds()
	if connected[0].GetKind() != "slack" || len(ch0) != 1 || ch0[0] != "C0SLACK" {
		t.Errorf("connected[0] = %+v, want {slack [C0SLACK]}", connected[0])
	}

	if connected[1].GetKind() != "teams" || len(connected[1].GetAlertChannelIds()) != 0 {
		t.Errorf("connected[1] = %+v, want {teams []}", connected[1])
	}
}

// ── ListConversations ─────────────────────────────────────────────────────────

func TestListConversations_HappyPath(t *testing.T) {
	t.Parallel()

	convID := uuid.New()
	svc := zeroServer()
	svc.listConversations = &fakeListConversations{
		summaries: []query.ConversationSummary{
			{ID: convID, Status: model.ConversationStatusOpen, Question: "what?"},
		},
	}

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.ListConversationsRequest{
		ProjectId: testProjectID.String(),
		Status:    "",
	})
	setAuth(req, "dev")

	resp, err := client.ListConversations(t.Context(), req)
	if err != nil {
		t.Fatalf("ListConversations returned error: %v", err)
	}

	if len(resp.Msg.GetConversations()) != 1 {
		t.Fatalf("conversations len = %d, want 1", len(resp.Msg.GetConversations()))
	}

	if resp.Msg.GetConversations()[0].GetId() != convID.String() {
		t.Errorf("conversation id = %q, want %q", resp.Msg.GetConversations()[0].GetId(), convID.String())
	}
}

func TestListConversations_InvalidProjectID(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.ListConversationsRequest{
		ProjectId: "not-a-uuid",
	})
	setAuth(req, "dev")

	_, err := client.ListConversations(t.Context(), req)
	assertConnectCode(t, err, connect.CodeInvalidArgument)
}

func TestListConversations_WrongProject_PermissionDenied(t *testing.T) {
	t.Parallel()

	otherProject := uuid.New()
	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.ListConversationsRequest{
		ProjectId: otherProject.String(),
	})
	setAuth(req, "dev")

	_, err := client.ListConversations(t.Context(), req)
	assertConnectCode(t, err, connect.CodePermissionDenied)
}

// ── ConfigureProvider ─────────────────────────────────────────────────────────

func TestConfigureProvider_HappyPath(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	svc.configureProvider = &fakeConfigureProvider{}

	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.ConfigureProviderRequest{
		ProjectId: testProjectID.String(),
		Kind:      "slack",
		Credentials: map[string]string{
			"bot_token":      "xoxb-test",
			"signing_secret": "s3cr3t",
		},
	})
	setAuthOrgAdmin(req, "admin")

	_, err := client.ConfigureProvider(t.Context(), req)
	if err != nil {
		t.Fatalf("ConfigureProvider returned error: %v", err)
	}
}

func TestConfigureProvider_AdminGate_DevRoleRejected(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.ConfigureProviderRequest{
		ProjectId: testProjectID.String(),
		Kind:      "slack",
		Credentials: map[string]string{
			"bot_token":      "xoxb-test",
			"signing_secret": "s3cr3t",
		},
	})
	setAuth(req, "dev") // dev role must be rejected by RBAC interceptor

	_, err := client.ConfigureProvider(t.Context(), req)
	assertConnectCode(t, err, connect.CodePermissionDenied)
}

func TestConfigureProvider_AdminGate_SpecialistRoleRejected(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.ConfigureProviderRequest{
		ProjectId: testProjectID.String(),
		Kind:      "slack",
		Credentials: map[string]string{
			"bot_token":      "xoxb-test",
			"signing_secret": "s3cr3t",
		},
	})
	setAuth(req, "specialist")

	_, err := client.ConfigureProvider(t.Context(), req)
	assertConnectCode(t, err, connect.CodePermissionDenied)
}

// A former project lead no longer passes the admin gate; only an org admin does.
func TestConfigureProvider_AdminGate_LeadRoleRejected(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.ConfigureProviderRequest{
		ProjectId: testProjectID.String(),
		Kind:      "slack",
		Credentials: map[string]string{
			"bot_token":      "xoxb-test",
			"signing_secret": "s3cr3t",
		},
	})
	setAuth(req, "lead") // lead is not an org admin

	_, err := client.ConfigureProvider(t.Context(), req)
	assertConnectCode(t, err, connect.CodePermissionDenied)
}

// An org admin (org-admin flag on the dev token) passes the admin gate.
func TestConfigureProvider_AdminGate_OrgAdminAllowed(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.ConfigureProviderRequest{
		ProjectId: testProjectID.String(),
		Kind:      "slack",
		Credentials: map[string]string{
			"bot_token":      "xoxb-test",
			"signing_secret": "s3cr3t",
		},
	})
	setAuthOrgAdmin(req, "specialist") // non-admin project role, but org admin

	_, err := client.ConfigureProvider(t.Context(), req)
	if err != nil {
		t.Fatalf("ConfigureProvider as org admin returned error: %v", err)
	}
}

func TestConfigureProvider_InvalidProjectID(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.ConfigureProviderRequest{
		ProjectId:   "not-a-uuid",
		Kind:        "slack",
		Credentials: map[string]string{"bot_token": "x", "signing_secret": "y"},
	})
	setAuthOrgAdmin(req, "admin")

	_, err := client.ConfigureProvider(t.Context(), req)
	assertConnectCode(t, err, connect.CodeInvalidArgument)
}

func TestConfigureProvider_Unauthenticated(t *testing.T) {
	t.Parallel()

	svc := zeroServer()
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	// No Authorization header.
	req := connect.NewRequest(&orakov1.ConfigureProviderRequest{
		ProjectId:   testProjectID.String(),
		Kind:        "slack",
		Credentials: map[string]string{"bot_token": "x"},
	})

	_, err := client.ConfigureProvider(t.Context(), req)
	assertConnectCode(t, err, connect.CodeUnauthenticated)
}

// ── Error code mapping ────────────────────────────────────────────────────────

// TestErrorCodeMapping verifies that each domain error type maps to the
// expected Connect status code via toConnectError. Uses CreateProject as the
// RPC surface (admin-gated so it exercises the full path).
func TestErrorCodeMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want connect.Code
	}{
		{
			name: "InvalidError → CodeInvalidArgument",
			err:  errs.InvalidError{Field: "name", Reason: "empty"},
			want: connect.CodeInvalidArgument,
		},
		{
			name: "NotFoundError → CodeNotFound",
			err:  errs.NotFoundError{Resource: "project"},
			want: connect.CodeNotFound,
		},
		{
			name: "DuplicateError → CodeAlreadyExists",
			err:  errs.DuplicateError{Resource: "project"},
			want: connect.CodeAlreadyExists,
		},
		{
			name: "ConflictError → CodeFailedPrecondition",
			err:  errs.ConflictError{},
			want: connect.CodeFailedPrecondition,
		},
		{
			name: "InternalError → CodeInternal",
			err:  errs.InternalError{Err: errors.New("db down")},
			want: connect.CodeInternal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := zeroServer()
			svc.createProject = &fakeCreateProject{err: tc.err}

			srv, client := startTestServerWithAuth(svc)
			defer srv.Close()

			req := connect.NewRequest(&orakov1.CreateProjectRequest{Name: "test"})
			setAuthOrgAdmin(req, "admin")

			_, err := client.CreateProject(t.Context(), req)
			assertConnectCode(t, err, tc.want)
		})
	}
}
