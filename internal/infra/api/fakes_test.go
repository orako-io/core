// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/infra/api/oauth"
)

// fakeProjectResolver satisfies projectOrgResolver. An empty resolver returns
// "not found" for every id, which keeps the cross-project org-admin path closed
// unless a test explicitly seeds a project→org mapping.
type fakeProjectResolver struct {
	byID     map[uuid.UUID]model.Project                `exhaustruct:"optional"`
	byMember map[uuid.UUID][]repository.ProjectWithRole `exhaustruct:"optional"`
	err      error                                      `exhaustruct:"optional"`
}

func (f *fakeProjectResolver) ByID(_ context.Context, id uuid.UUID) (model.Project, error) {
	if f.err != nil {
		return model.Project{}, f.err
	}

	p, ok := f.byID[id]
	if !ok {
		return model.Project{}, adaptererr.ErrNotFound
	}

	return p, nil
}

func (f *fakeProjectResolver) ProjectsByMember(_ context.Context, memberID uuid.UUID) ([]repository.ProjectWithRole, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.byMember[memberID], nil
}

// ── Command fakes ────────────────────────────────────────────────────────────

type fakeListExperts struct {
	experts []query.Expert
	err     error `exhaustruct:"optional"`
}

func (f *fakeListExperts) Handle(_ context.Context, _ query.ListExpertsQuery) ([]query.Expert, error) {
	return f.experts, f.err
}

type fakeAsk struct {
	result command.AskResult
	err    error `exhaustruct:"optional"`
}

func (f *fakeAsk) Handle(_ context.Context, _ command.AskCommand) (command.AskResult, error) {
	return f.result, f.err
}

type fakeGetConversation struct {
	view query.ConversationView
	err  error `exhaustruct:"optional"`
}

func (f *fakeGetConversation) Handle(_ context.Context, _ query.GetConversationQuery) (query.ConversationView, error) {
	return f.view, f.err
}

type fakeFollowUp struct {
	err  error                   `exhaustruct:"optional"`
	last command.FollowUpCommand `exhaustruct:"optional"`
}

func (f *fakeFollowUp) Handle(_ context.Context, cmd command.FollowUpCommand) (command.FollowUpResult, error) {
	f.last = cmd

	return command.FollowUpResult{}, f.err
}

type fakeGetOrgSettings struct {
	view query.OrgSettingsView `exhaustruct:"optional"`
	err  error                 `exhaustruct:"optional"`
}

func (f *fakeGetOrgSettings) Handle(_ context.Context, _ query.GetOrganizationSettingsQuery) (query.OrgSettingsView, error) {
	return f.view, f.err
}

type fakeUpdateOrgSettings struct {
	err error `exhaustruct:"optional"`
}

func (f *fakeUpdateOrgSettings) Handle(_ context.Context, _ command.UpdateOrganizationSettingsCommand) error {
	return f.err
}

type fakeGetOrganization struct {
	view query.OrgIdentityView `exhaustruct:"optional"`
	err  error                 `exhaustruct:"optional"`
}

func (f *fakeGetOrganization) Handle(_ context.Context, _ query.GetOrganizationQuery) (query.OrgIdentityView, error) {
	return f.view, f.err
}

type fakeRenameOrganization struct {
	result command.RenameOrganizationResult `exhaustruct:"optional"`
	err    error                            `exhaustruct:"optional"`
	got    command.RenameOrganizationCommand
}

func (f *fakeRenameOrganization) Handle(_ context.Context, cmd command.RenameOrganizationCommand) (command.RenameOrganizationResult, error) {
	f.got = cmd

	return f.result, f.err
}

type fakeDeleteOrganization struct {
	result command.DeleteOrganizationResult `exhaustruct:"optional"`
	err    error                            `exhaustruct:"optional"`
	got    command.DeleteOrganizationCommand
}

func (f *fakeDeleteOrganization) Handle(_ context.Context, cmd command.DeleteOrganizationCommand) (command.DeleteOrganizationResult, error) {
	f.got = cmd

	return f.result, f.err
}

type fakeListOrganizations struct {
	views []query.OrganizationView `exhaustruct:"optional"`
	err   error                    `exhaustruct:"optional"`
}

func (f *fakeListOrganizations) Handle(_ context.Context, _ query.ListOrganizationsQuery) ([]query.OrganizationView, error) {
	return f.views, f.err
}

type fakeResolveConversation struct {
	err    error `exhaustruct:"optional"`
	gotCmd command.ResolveConversationCommand
}

func (f *fakeResolveConversation) Handle(_ context.Context, cmd command.ResolveConversationCommand) (command.ResolveConversationResult, error) {
	f.gotCmd = cmd

	return command.ResolveConversationResult{}, f.err
}

type fakeDismissConversation struct {
	result command.DismissConversationResult  `exhaustruct:"optional"`
	err    error                              `exhaustruct:"optional"`
	gotCmd command.DismissConversationCommand `exhaustruct:"optional"`
}

func (f *fakeDismissConversation) Handle(_ context.Context, cmd command.DismissConversationCommand) (command.DismissConversationResult, error) {
	f.gotCmd = cmd

	return f.result, f.err
}

type fakeHeartbeat struct {
	err error `exhaustruct:"optional"`
}

func (f *fakeHeartbeat) Handle(_ context.Context, _ command.HeartbeatCommand) error {
	return f.err
}

type fakeCreateOrganization struct {
	result command.CreateOrganizationResult
	err    error `exhaustruct:"optional"`
}

func (f *fakeCreateOrganization) Handle(_ context.Context, _ command.CreateOrganizationCommand) (command.CreateOrganizationResult, error) {
	return f.result, f.err
}

type fakeCreateProject struct {
	result command.CreateProjectResult
	err    error `exhaustruct:"optional"`
}

func (f *fakeCreateProject) Handle(_ context.Context, _ command.CreateProjectCommand) (command.CreateProjectResult, error) {
	return f.result, f.err
}

type fakeRenameProject struct {
	err     error                        `exhaustruct:"optional"`
	lastCmd command.RenameProjectCommand `exhaustruct:"optional"`
}

func (f *fakeRenameProject) Handle(_ context.Context, cmd command.RenameProjectCommand) (command.RenameProjectResult, error) {
	f.lastCmd = cmd
	return command.RenameProjectResult{}, f.err
}

type fakeSetProjectArchived struct {
	err     error                             `exhaustruct:"optional"`
	lastCmd command.SetProjectArchivedCommand `exhaustruct:"optional"`
}

func (f *fakeSetProjectArchived) Handle(_ context.Context, cmd command.SetProjectArchivedCommand) (command.SetProjectArchivedResult, error) {
	f.lastCmd = cmd
	return command.SetProjectArchivedResult{}, f.err
}

type fakeDeleteProject struct {
	err     error                        `exhaustruct:"optional"`
	lastCmd command.DeleteProjectCommand `exhaustruct:"optional"`
}

func (f *fakeDeleteProject) Handle(_ context.Context, cmd command.DeleteProjectCommand) (command.DeleteProjectResult, error) {
	f.lastCmd = cmd
	return command.DeleteProjectResult{}, f.err
}

type fakeDeleteConversation struct {
	err     error                             `exhaustruct:"optional"`
	lastCmd command.DeleteConversationCommand `exhaustruct:"optional"`
}

func (f *fakeDeleteConversation) Handle(_ context.Context, cmd command.DeleteConversationCommand) (command.DeleteConversationResult, error) {
	f.lastCmd = cmd
	return command.DeleteConversationResult{}, f.err
}

// fakeConvScope backs requireConversationAccess. It defaults to a conversation
// in testProjectID so a default dev caller (member of testProjectID) passes the
// guard; set projectID/err to exercise the deny paths.
type fakeConvScope struct {
	projectID uuid.UUID `exhaustruct:"optional"`
	err       error     `exhaustruct:"optional"`
}

func (f *fakeConvScope) ConversationByID(_ context.Context, _ uuid.UUID) (model.Conversation, error) {
	if f.err != nil {
		return model.Conversation{}, f.err
	}

	pid := f.projectID
	if pid == uuid.Nil {
		pid = testProjectID
	}

	return model.Conversation{ProjectID: pid}, nil
}

type fakeListProjectsDetailed struct {
	details []query.ProjectDetail
	err     error `exhaustruct:"optional"`
}

func (f *fakeListProjectsDetailed) Handle(_ context.Context, _ query.ListProjectsDetailedQuery) ([]query.ProjectDetail, error) {
	return f.details, f.err
}

type fakeAddMember struct {
	result  command.AddMemberResult
	err     error                    `exhaustruct:"optional"`
	lastCmd command.AddMemberCommand `exhaustruct:"optional"`
}

func (f *fakeAddMember) Handle(_ context.Context, cmd command.AddMemberCommand) (command.AddMemberResult, error) {
	f.lastCmd = cmd
	return f.result, f.err
}

type fakeInviteMembers struct {
	result command.InviteMembersResult `exhaustruct:"optional"`
	err    error                       `exhaustruct:"optional"`
}

func (f *fakeInviteMembers) Handle(_ context.Context, _ command.InviteMembersCommand) (command.InviteMembersResult, error) {
	return f.result, f.err
}

type fakeAssignRole struct {
	err     error                     `exhaustruct:"optional"`
	lastCmd command.AssignRoleCommand `exhaustruct:"optional"`
}

func (f *fakeAssignRole) Handle(_ context.Context, cmd command.AssignRoleCommand) error {
	f.lastCmd = cmd
	return f.err
}

type fakeSetOwnExpertise struct {
	err     error                          `exhaustruct:"optional"`
	lastCmd command.SetOwnExpertiseCommand `exhaustruct:"optional"`
}

func (f *fakeSetOwnExpertise) Handle(_ context.Context, cmd command.SetOwnExpertiseCommand) error {
	f.lastCmd = cmd
	return f.err
}

type fakeRemoveMember struct {
	lastCmd command.RemoveMemberCommand `exhaustruct:"optional"`
	err     error                       `exhaustruct:"optional"`
}

func (f *fakeRemoveMember) Handle(_ context.Context, cmd command.RemoveMemberCommand) error {
	f.lastCmd = cmd
	return f.err
}

type fakeConfigureProvider struct {
	result command.ConfigureProviderResult
	err    error `exhaustruct:"optional"`
}

func (f *fakeConfigureProvider) Handle(_ context.Context, _ command.ConfigureProviderCommand) (command.ConfigureProviderResult, error) {
	return f.result, f.err
}

type fakeDisconnectProvider struct {
	result command.DisconnectProviderResult `exhaustruct:"optional"`
	err    error                            `exhaustruct:"optional"`
}

func (f *fakeDisconnectProvider) Handle(_ context.Context, _ command.DisconnectProviderCommand) (command.DisconnectProviderResult, error) {
	return f.result, f.err
}

type fakeSyncChatBindings struct {
	result command.SyncChatBindingsResult `exhaustruct:"optional"`
	err    error                          `exhaustruct:"optional"`
}

func (f *fakeSyncChatBindings) Handle(_ context.Context, _ command.SyncChatBindingsCommand) (command.SyncChatBindingsResult, error) {
	return f.result, f.err
}

type fakeListProjects struct {
	summaries []query.ProjectSummary
	err       error `exhaustruct:"optional"`
}

func (f *fakeListProjects) Handle(_ context.Context, _ query.ListProjectsQuery) ([]query.ProjectSummary, error) {
	return f.summaries, f.err
}

type fakeListConversations struct {
	summaries []query.ConversationSummary
	err       error `exhaustruct:"optional"`
}

func (f *fakeListConversations) Handle(_ context.Context, _ query.ListConversationsQuery) ([]query.ConversationSummary, error) {
	return f.summaries, f.err
}

type fakeListInbox struct {
	items []query.InboxItem
	err   error `exhaustruct:"optional"`
}

func (f *fakeListInbox) Handle(_ context.Context, _ query.ListInboxQuery) ([]query.InboxItem, error) {
	return f.items, f.err
}

type fakeGetMember struct {
	view query.MemberView
	err  error `exhaustruct:"optional"`
}

func (f *fakeGetMember) Handle(_ context.Context, _ query.GetMemberQuery) (query.MemberView, error) {
	return f.view, f.err
}

type fakeUpdateMember struct {
	member command.UpdateMemberResult
	err    error `exhaustruct:"optional"`
}

func (f *fakeUpdateMember) Handle(_ context.Context, _ command.UpdateMemberCommand) (command.UpdateMemberResult, error) {
	return f.member, f.err
}

type fakeListConnectedChannels struct {
	channels []string
	err      error `exhaustruct:"optional"`
}

func (f *fakeListConnectedChannels) Handle(_ context.Context, _ query.ListConnectedChannelsQuery) ([]string, error) {
	return f.channels, f.err
}

type fakeGetProviderAlertChannels struct {
	channels []service.ProviderAlertChannel
	err      error `exhaustruct:"optional"`
}

func (f *fakeGetProviderAlertChannels) Handle(_ context.Context, _ query.GetProviderAlertChannelsQuery) ([]service.ProviderAlertChannel, error) {
	return f.channels, f.err
}

// ── Provider fake ────────────────────────────────────────────────────────────

// fakeProvider implements service.Provider for webhook tests.
type fakeProvider struct {
	inbound service.InboundMessage
	err     error `exhaustruct:"optional"`
}

func (f *fakeProvider) Deliver(_ context.Context, _ service.OutboundMessage) (service.MessageRef, error) {
	return service.MessageRef{}, nil
}

func (f *fakeProvider) ParseInbound(_ context.Context, _ []byte) (service.InboundMessage, error) {
	return f.inbound, f.err
}

// fakeProviderCredentials is a providerCredentialsLoader that never resolves
// credentials (bot_client_id stays empty in tests).
type fakeProviderCredentials struct{}

func (fakeProviderCredentials) LoadProvider(context.Context, uuid.UUID, string) ([]byte, error) {
	return nil, errors.New("no credentials in tests")
}

// ── Server builder ───────────────────────────────────────────────────────────

// newTestServer constructs a Server with all fakes pre-wired. Zero-value fakes
// are safe to use (they return their zero/nil fields).
func newTestServer(
	ls *fakeListExperts,
	as *fakeAsk,
	gc *fakeGetConversation,
	fu *fakeFollowUp,
	cc *fakeResolveConversation,
	lp *fakeListProjects,
	rp *fakeRenameProject,
	spa *fakeSetProjectArchived,
	dp *fakeDeleteProject,
	lpd *fakeListProjectsDetailed,
	lc *fakeListConversations,
	li *fakeListInbox,
	gm *fakeGetMember,
	um *fakeUpdateMember,
	lcc *fakeListConnectedChannels,
	gpac *fakeGetProviderAlertChannels,
	hb *fakeHeartbeat,
	co *fakeCreateOrganization,
	cp *fakeCreateProject,
	am *fakeAddMember,
	ar *fakeAssignRole,
	rm *fakeRemoveMember,
	cfgp *fakeConfigureProvider,
) *Server {
	return &Server{
		listExperts:              ls,
		ask:                      as,
		getConversation:          gc,
		followUp:                 fu,
		resolveConversation:      cc,
		dismissConversation:      &fakeDismissConversation{},
		listProjects:             lp,
		renameProject:            rp,
		setProjectArchived:       spa,
		deleteProject:            dp,
		deleteConversation:       &fakeDeleteConversation{},
		listProjectsDetailed:     lpd,
		listConversations:        lc,
		searchHistory:            &fakeSearchHistory{},
		historyCounts:            &fakeHistoryCounts{},
		listKnowledge:            &fakeListKnowledge{},
		createKnowledge:          &fakeCreateKnowledge{},
		updateKnowledge:          &fakeUpdateKnowledge{},
		markKnowledgeStale:       &fakeMarkKnowledgeStale{},
		revalidateKnowledge:      &fakeRevalidateKnowledge{},
		listPendingKnowledge:     &fakeListPendingKnowledge{},
		approveKnowledge:         &fakeApproveKnowledge{},
		dismissKnowledge:         &fakeDismissKnowledge{},
		promoteConversation:      &fakePromoteConversation{},
		dashboardMetrics:         &fakeDashboardMetrics{},
		listInbox:                li,
		getMember:                gm,
		updateMember:             um,
		listMembers:              &fakeListMembers{},
		getOrgMember:             &fakeGetOrgMember{},
		setMemberAvailability:    &fakeSetMemberAvailability{},
		setMemberActivation:      &fakeSetMemberActivation{},
		setOrgAdmin:              &fakeSetOrgAdmin{},
		listConnectedChannels:    lcc,
		providerCredentials:      fakeProviderCredentials{},
		getProviderAlertChannels: gpac,
		listMcpConnections:       &fakeListMcpConnections{},
		revokeMcpConnection:      &fakeRevokeMcpConnection{},
		heartbeat:                hb,
		createOrganization:       co,
		createProject:            cp,
		addMember:                am,
		inviteMembers:            &fakeInviteMembers{},
		assignRole:               ar,
		setOwnExpertise:          &fakeSetOwnExpertise{},
		removeMember:             rm,
		configureProvider:        cfgp,
		syncChatBindings:         &fakeSyncChatBindings{},
		disconnectProvider:       &fakeDisconnectProvider{},
		sendProviderTest:         &fakeSendProviderTest{},
		getOrgSettings:           &fakeGetOrgSettings{},
		updateOrgSettings:        &fakeUpdateOrgSettings{},
		renameOrganization:       &fakeRenameOrganization{},
		deleteOrganization:       &fakeDeleteOrganization{},
		getOrganization:          &fakeGetOrganization{},
		listOrganizations:        &fakeListOrganizations{},
		generateJoinCode:         &fakeGenerateJoinCode{},
		getJoinCode:              &fakeGetJoinCode{},
		revokeJoinCode:           &fakeRevokeJoinCode{},
		createMachineToken:       &fakeCreateMachineToken{},
		listMachineTokens:        &fakeListMachineTokens{},
		revokeMachineToken:       &fakeRevokeMachineToken{},
		projects:                 seededProjectResolver(),
		convScope:                &fakeConvScope{},
		logger:                   newTestLogger(),
	}
}

// Join-code fakes: no-op stubs so the shared test server satisfies the handler
// interface; they capture the received command/query so join_code_test.go can
// assert the org comes from the caller identity, not the request body.
type fakeGenerateJoinCode struct {
	result command.GenerateJoinCodeResult `exhaustruct:"optional"`
	err    error                          `exhaustruct:"optional"`
	got    command.GenerateJoinCodeCommand
}

func (f *fakeGenerateJoinCode) Handle(_ context.Context, cmd command.GenerateJoinCodeCommand) (command.GenerateJoinCodeResult, error) {
	f.got = cmd

	return f.result, f.err
}

type fakeGetJoinCode struct {
	view query.JoinCodeView `exhaustruct:"optional"`
	err  error              `exhaustruct:"optional"`
	got  query.GetJoinCodeQuery
}

func (f *fakeGetJoinCode) Handle(_ context.Context, q query.GetJoinCodeQuery) (query.JoinCodeView, error) {
	f.got = q

	return f.view, f.err
}

type fakeRevokeJoinCode struct {
	err error `exhaustruct:"optional"`
	got command.RevokeJoinCodeCommand
}

func (f *fakeRevokeJoinCode) Handle(_ context.Context, cmd command.RevokeJoinCodeCommand) error {
	f.got = cmd

	return f.err
}

// Machine-token fakes (phase 1): no-op stubs so the shared test server
// satisfies the handler interfaces; capture the received command/query so a
// dedicated test can assert the caller identity (never the request body)
// supplies MemberID/OrgID/IsOrgAdmin.
type fakeCreateMachineToken struct {
	result command.CreateMachineTokenResult `exhaustruct:"optional"`
	err    error                            `exhaustruct:"optional"`
	got    command.CreateMachineTokenCommand
}

func (f *fakeCreateMachineToken) Handle(_ context.Context, cmd command.CreateMachineTokenCommand) (command.CreateMachineTokenResult, error) {
	f.got = cmd

	return f.result, f.err
}

type fakeListMachineTokens struct {
	views []query.MachineTokenView `exhaustruct:"optional"`
	err   error                    `exhaustruct:"optional"`
	got   query.ListMachineTokensQuery
}

func (f *fakeListMachineTokens) Handle(_ context.Context, q query.ListMachineTokensQuery) ([]query.MachineTokenView, error) {
	f.got = q

	return f.views, f.err
}

type fakeRevokeMachineToken struct {
	err error `exhaustruct:"optional"`
	got command.RevokeMachineTokenCommand
}

func (f *fakeRevokeMachineToken) Handle(_ context.Context, cmd command.RevokeMachineTokenCommand) error {
	f.got = cmd

	return f.err
}

// Curated-knowledge fakes: no-op stubs so the shared test server satisfies the
// handler interfaces; knowledge_test.go swaps in capturing variants where it
// asserts behavior.
type fakeListKnowledge struct {
	entries []query.KnowledgeEntryView `exhaustruct:"optional"`
	err     error                      `exhaustruct:"optional"`
}

func (f *fakeListKnowledge) Handle(_ context.Context, _ query.ListKnowledgeEntriesQuery) ([]query.KnowledgeEntryView, error) {
	return f.entries, f.err
}

type fakeCreateKnowledge struct {
	result command.CreateKnowledgeEntryResult `exhaustruct:"optional"`
	err    error                              `exhaustruct:"optional"`
	got    command.CreateKnowledgeEntryCommand
}

func (f *fakeCreateKnowledge) Handle(_ context.Context, cmd command.CreateKnowledgeEntryCommand) (command.CreateKnowledgeEntryResult, error) {
	f.got = cmd

	return f.result, f.err
}

type fakeUpdateKnowledge struct {
	err error `exhaustruct:"optional"`
	got command.UpdateKnowledgeEntryCommand
}

func (f *fakeUpdateKnowledge) Handle(_ context.Context, cmd command.UpdateKnowledgeEntryCommand) (command.UpdateKnowledgeEntryResult, error) {
	f.got = cmd

	return command.UpdateKnowledgeEntryResult{}, f.err
}

type fakeMarkKnowledgeStale struct {
	err error `exhaustruct:"optional"`
	got command.MarkKnowledgeStaleCommand
}

func (f *fakeMarkKnowledgeStale) Handle(_ context.Context, cmd command.MarkKnowledgeStaleCommand) (command.MarkKnowledgeStaleResult, error) {
	f.got = cmd

	return command.MarkKnowledgeStaleResult{}, f.err
}

type fakeRevalidateKnowledge struct {
	err error `exhaustruct:"optional"`
	got command.RevalidateKnowledgeEntryCommand
}

func (f *fakeRevalidateKnowledge) Handle(_ context.Context, cmd command.RevalidateKnowledgeEntryCommand) (command.RevalidateKnowledgeEntryResult, error) {
	f.got = cmd

	return command.RevalidateKnowledgeEntryResult{}, f.err
}

type fakeListPendingKnowledge struct {
	entries []query.KnowledgeEntryView `exhaustruct:"optional"`
	err     error                      `exhaustruct:"optional"`
	got     query.ListPendingKnowledgeEntriesQuery
}

func (f *fakeListPendingKnowledge) Handle(_ context.Context, q query.ListPendingKnowledgeEntriesQuery) ([]query.KnowledgeEntryView, error) {
	f.got = q

	return f.entries, f.err
}

type fakeApproveKnowledge struct {
	err error `exhaustruct:"optional"`
	got command.ApproveKnowledgeEntryCommand
}

func (f *fakeApproveKnowledge) Handle(_ context.Context, cmd command.ApproveKnowledgeEntryCommand) (command.ApproveKnowledgeEntryResult, error) {
	f.got = cmd

	return command.ApproveKnowledgeEntryResult{}, f.err
}

type fakeDismissKnowledge struct {
	err error `exhaustruct:"optional"`
	got command.DismissKnowledgeEntryCommand
}

func (f *fakeDismissKnowledge) Handle(_ context.Context, cmd command.DismissKnowledgeEntryCommand) (command.DismissKnowledgeEntryResult, error) {
	f.got = cmd

	return command.DismissKnowledgeEntryResult{}, f.err
}

type fakePromoteConversation struct {
	result command.PromoteConversationToKnowledgeResult `exhaustruct:"optional"`
	err    error                                        `exhaustruct:"optional"`
	got    command.PromoteConversationToKnowledgeCommand
}

func (f *fakePromoteConversation) Handle(_ context.Context, cmd command.PromoteConversationToKnowledgeCommand) (command.PromoteConversationToKnowledgeResult, error) {
	f.got = cmd

	return f.result, f.err
}

// History-tab fakes: the shared test server only needs no-op implementations.
type fakeSearchHistory struct {
	hits []query.HistoryHit `exhaustruct:"optional"`
	err  error              `exhaustruct:"optional"`
}

func (f *fakeSearchHistory) Handle(_ context.Context, _ query.SearchHistoryQuery) ([]query.HistoryHit, error) {
	return f.hits, f.err
}

type fakeHistoryCounts struct {
	counts query.HistoryStatusCounts `exhaustruct:"optional"`
	err    error                     `exhaustruct:"optional"`
}

func (f *fakeHistoryCounts) Handle(_ context.Context, _ query.HistoryStatusCountsQuery) (query.HistoryStatusCounts, error) {
	return f.counts, f.err
}

type fakeDashboardMetrics struct {
	metrics query.DashboardMetrics `exhaustruct:"optional"`
	err     error                  `exhaustruct:"optional"`
}

func (f *fakeDashboardMetrics) Handle(_ context.Context, _ query.DashboardMetricsQuery) (query.DashboardMetrics, error) {
	return f.metrics, f.err
}

// MCP-connection fakes: the shared test server only needs no-op
// implementations; a dedicated test file covers the RPCs themselves.
type fakeListMcpConnections struct {
	grants []oauth.Grant
	err    error `exhaustruct:"optional"`
}

func (f *fakeListMcpConnections) ListGrantsByMember(_ context.Context, _, _ uuid.UUID) ([]oauth.Grant, error) {
	return f.grants, f.err
}

type fakeRevokeMcpConnection struct {
	err error `exhaustruct:"optional"`
}

func (f *fakeRevokeMcpConnection) RevokeGrantForMember(_ context.Context, _, _, _ uuid.UUID) error {
	return f.err
}

// zeroServer returns a Server with all no-op fakes. Use when only one or two
// handlers need to be controlled.
func zeroServer() *Server {
	return newTestServer(
		&fakeListExperts{},
		&fakeAsk{result: command.AskResult{ConversationID: uuid.New()}},
		&fakeGetConversation{view: query.ConversationView{
			ID:     uuid.New(),
			Status: model.ConversationStatusOpen,
		}},
		&fakeFollowUp{},
		&fakeResolveConversation{},
		&fakeListProjects{},
		&fakeRenameProject{},
		&fakeSetProjectArchived{},
		&fakeDeleteProject{},
		&fakeListProjectsDetailed{},
		&fakeListConversations{},
		&fakeListInbox{},
		&fakeGetMember{},
		&fakeUpdateMember{},
		&fakeListConnectedChannels{},
		&fakeGetProviderAlertChannels{},
		&fakeHeartbeat{},
		&fakeCreateOrganization{result: command.CreateOrganizationResult{OrgID: uuid.New(), GlobalProjectID: uuid.New()}},
		&fakeCreateProject{result: command.CreateProjectResult{ProjectID: uuid.New()}},
		&fakeAddMember{result: command.AddMemberResult{MemberID: uuid.New()}},
		&fakeAssignRole{},
		&fakeRemoveMember{},
		&fakeConfigureProvider{},
	)
}

// Member-administration fakes: no-op stubs so the shared test server satisfies
// the handler interface; the member-admin flows have dedicated tests.
type fakeListMembers struct{}

func (f *fakeListMembers) Handle(_ context.Context, _ query.ListMembersQuery) ([]query.OrgMemberView, error) {
	return nil, nil
}

type fakeGetOrgMember struct{}

func (f *fakeGetOrgMember) Handle(_ context.Context, _ query.GetOrgMemberQuery) (query.OrgMemberView, error) {
	return query.OrgMemberView{}, nil
}

type fakeSetMemberAvailability struct{}

func (f *fakeSetMemberAvailability) Handle(_ context.Context, _ command.SetMemberAvailabilityCommand) error {
	return nil
}

type fakeSetMemberActivation struct{}

func (f *fakeSetMemberActivation) Handle(_ context.Context, _ command.SetMemberActivationCommand) error {
	return nil
}

type fakeSetOrgAdmin struct{}

func (f *fakeSetOrgAdmin) Handle(_ context.Context, _ command.SetOrgAdminCommand) error { return nil }

type fakeSendProviderTest struct{}

func (f *fakeSendProviderTest) Handle(_ context.Context, _ command.SendProviderTestCommand) ([]command.ProviderTestResult, error) {
	return nil, nil
}
