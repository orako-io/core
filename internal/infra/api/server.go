// SPDX-License-Identifier: AGPL-3.0-or-later

// Package api is the Connect-RPC driving adapter for the Orako server.
// It is pure translation glue between proto requests and the application
// command/query handlers; no business logic lives here.
package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"google.golang.org/protobuf/types/known/timestamppb"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/gen/orako/v1/orakov1connect"
	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/infra/api/oauth"
)

// Narrow handler interfaces — one per application-layer handler the Server
// depends on; fakes implement them in tests.

// projectOrgResolver resolves a project's owning organization. It lets an org
// admin read a project in their own org even when it is not their active
// project. Satisfied by *identity.ProjectStore.
type projectOrgResolver interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Project, error)
	// ProjectsByMember lists a member's projects (with their owning org), used
	// to confirm a target member shares the caller's org before an admin RPC
	// acts on them.
	ProjectsByMember(ctx context.Context, memberID uuid.UUID) ([]repository.ProjectWithRole, error)
}

// conversationScopeReader resolves a conversation's project so member-action
// RPCs that take only a conversation_id (Close/Dismiss) can verify the caller
// belongs to that project before mutating. Satisfied by *conversation.Store.
type conversationScopeReader interface {
	ConversationByID(ctx context.Context, id uuid.UUID) (model.Conversation, error)
}

type listExpertser interface {
	Handle(ctx context.Context, q query.ListExpertsQuery) ([]query.Expert, error)
}

type conversationAsker interface {
	Handle(ctx context.Context, cmd command.AskCommand) (command.AskResult, error)
}

type getConversationer interface {
	Handle(ctx context.Context, q query.GetConversationQuery) (query.ConversationView, error)
}

type followUpper interface {
	Handle(ctx context.Context, cmd command.FollowUpCommand) (command.FollowUpResult, error)
}

type resolveConversationer interface {
	Handle(ctx context.Context, cmd command.ResolveConversationCommand) (command.ResolveConversationResult, error)
}

type dismisser interface {
	Handle(ctx context.Context, cmd command.DismissConversationCommand) (command.DismissConversationResult, error)
}

type heartbeater interface {
	Handle(ctx context.Context, cmd command.HeartbeatCommand) error
}

type organizationCreator interface {
	Handle(ctx context.Context, cmd command.CreateOrganizationCommand) (command.CreateOrganizationResult, error)
}

type projectCreator interface {
	Handle(ctx context.Context, cmd command.CreateProjectCommand) (command.CreateProjectResult, error)
}

type projectRenamer interface {
	Handle(ctx context.Context, cmd command.RenameProjectCommand) (command.RenameProjectResult, error)
}

type projectArchiveSetter interface {
	Handle(ctx context.Context, cmd command.SetProjectArchivedCommand) (command.SetProjectArchivedResult, error)
}

type projectDeleter interface {
	Handle(ctx context.Context, cmd command.DeleteProjectCommand) (command.DeleteProjectResult, error)
}

type conversationDeleter interface {
	Handle(ctx context.Context, cmd command.DeleteConversationCommand) (command.DeleteConversationResult, error)
}

type projectsDetailedLister interface {
	Handle(ctx context.Context, q query.ListProjectsDetailedQuery) ([]query.ProjectDetail, error)
}

type memberAdder interface {
	Handle(ctx context.Context, cmd command.AddMemberCommand) (command.AddMemberResult, error)
}

type membersInviter interface {
	Handle(ctx context.Context, cmd command.InviteMembersCommand) (command.InviteMembersResult, error)
}

type roleAssigner interface {
	Handle(ctx context.Context, cmd command.AssignRoleCommand) error
}

type ownExpertiseSetter interface {
	Handle(ctx context.Context, memberID uuid.UUID, domains []string) error
}

type memberRemover interface {
	Handle(ctx context.Context, cmd command.RemoveMemberCommand) error
}

type providerConfigurer interface {
	Handle(ctx context.Context, cmd command.ConfigureProviderCommand) (command.ConfigureProviderResult, error)
}

type providerDisconnecter interface {
	Handle(ctx context.Context, cmd command.DisconnectProviderCommand) (command.DisconnectProviderResult, error)
}

type chatDirectorySyncer interface {
	Handle(ctx context.Context, cmd command.SyncChatBindingsCommand) (command.SyncChatBindingsResult, error)
}

type providerTester interface {
	Handle(ctx context.Context, cmd command.SendProviderTestCommand) ([]command.ProviderTestResult, error)
}

type projectsLister interface {
	Handle(ctx context.Context, q query.ListProjectsQuery) ([]query.ProjectSummary, error)
}

type conversationsLister interface {
	Handle(ctx context.Context, q query.ListConversationsQuery) ([]query.ConversationSummary, error)
}

type historySearcher interface {
	Handle(ctx context.Context, q query.SearchHistoryQuery) ([]query.HistoryHit, error)
}

type historyCounter interface {
	Handle(ctx context.Context, q query.HistoryStatusCountsQuery) (query.HistoryStatusCounts, error)
}

type knowledgeLister interface {
	Handle(ctx context.Context, q query.ListKnowledgeEntriesQuery) ([]query.KnowledgeEntryView, error)
}

type knowledgeCreator interface {
	Handle(ctx context.Context, cmd command.CreateKnowledgeEntryCommand) (command.CreateKnowledgeEntryResult, error)
}

type knowledgeUpdater interface {
	Handle(ctx context.Context, cmd command.UpdateKnowledgeEntryCommand) (command.UpdateKnowledgeEntryResult, error)
}

type knowledgeStaleMarker interface {
	Handle(ctx context.Context, cmd command.MarkKnowledgeStaleCommand) (command.MarkKnowledgeStaleResult, error)
}

type knowledgeRevalidator interface {
	Handle(ctx context.Context, cmd command.RevalidateKnowledgeEntryCommand) (command.RevalidateKnowledgeEntryResult, error)
}

type pendingKnowledgeLister interface {
	Handle(ctx context.Context, q query.ListPendingKnowledgeEntriesQuery) ([]query.KnowledgeEntryView, error)
}

type knowledgeApprover interface {
	Handle(ctx context.Context, cmd command.ApproveKnowledgeEntryCommand) (command.ApproveKnowledgeEntryResult, error)
}

type knowledgeDismisser interface {
	Handle(ctx context.Context, cmd command.DismissKnowledgeEntryCommand) (command.DismissKnowledgeEntryResult, error)
}

type conversationPromoter interface {
	Handle(ctx context.Context, cmd command.PromoteConversationToKnowledgeCommand) (command.PromoteConversationToKnowledgeResult, error)
}

type dashboardMetricser interface {
	Handle(ctx context.Context, q query.DashboardMetricsQuery) (query.DashboardMetrics, error)
}

type inboxLister interface {
	Handle(ctx context.Context, q query.ListInboxQuery) ([]query.InboxItem, error)
}

type memberGetter interface {
	Handle(ctx context.Context, q query.GetMemberQuery) (query.MemberView, error)
}

type memberUpdater interface {
	Handle(ctx context.Context, cmd command.UpdateMemberCommand) (model.Member, error)
}

type membersLister interface {
	Handle(ctx context.Context, q query.ListMembersQuery) ([]query.OrgMemberView, error)
}

type orgMemberGetter interface {
	Handle(ctx context.Context, q query.GetOrgMemberQuery) (query.OrgMemberView, error)
}

type memberAvailabilitySetter interface {
	Handle(ctx context.Context, cmd command.SetMemberAvailabilityCommand) error
}

type memberActivationSetter interface {
	Handle(ctx context.Context, cmd command.SetMemberActivationCommand) error
}

type orgAdminSetter interface {
	Handle(ctx context.Context, cmd command.SetOrgAdminCommand) error
}

// providerCredentialsLoader resolves a project's stored provider credentials
// (org-level with project fallback). Used ONLY to derive the Discord bot's
// public application id for the manage page's re-invite link — the token
// itself never crosses the wire. *eventlog.OrgResolvingProviderLoader
// satisfies it.
type providerCredentialsLoader interface {
	LoadProvider(ctx context.Context, projectID uuid.UUID, kind string) ([]byte, error)
}

type connectedChannelsLister interface {
	Handle(ctx context.Context, q query.ListConnectedChannelsQuery) ([]string, error)
}

// providerAlertChannelsGetter resolves a project's configured providers with
// their stored alert channels, so ListConnectedChannels can populate the
// `connected` field without blanking an unrelated field on credential re-save.
type providerAlertChannelsGetter interface {
	Handle(ctx context.Context, q query.GetProviderAlertChannelsQuery) ([]service.ProviderAlertChannel, error)
}

// mcpConnectionLister resolves the caller's live MCP connections (one per
// grant_id). Satisfied by *oauth.Store.
type mcpConnectionLister interface {
	ListGrantsByMember(ctx context.Context, memberID uuid.UUID) ([]oauth.Grant, error)
}

// mcpConnectionRevoker disconnects one of the caller's MCP connections,
// scoped to the caller so it can never revoke another member's grant.
// Satisfied by *oauth.Store.
type mcpConnectionRevoker interface {
	RevokeGrantForMember(ctx context.Context, memberID, grantID uuid.UUID) error
}

type orgSettingsGetter interface {
	Handle(ctx context.Context, q query.GetOrganizationSettingsQuery) (query.OrgSettingsView, error)
}

type orgSettingsUpdater interface {
	Handle(ctx context.Context, cmd command.UpdateOrganizationSettingsCommand) error
}

type organizationRenamer interface {
	Handle(ctx context.Context, cmd command.RenameOrganizationCommand) (command.RenameOrganizationResult, error)
}

type organizationDeleter interface {
	Handle(ctx context.Context, cmd command.DeleteOrganizationCommand) (command.DeleteOrganizationResult, error)
}

type organizationGetter interface {
	Handle(ctx context.Context, q query.GetOrganizationQuery) (query.OrgIdentityView, error)
}

type organizationsLister interface {
	Handle(ctx context.Context, q query.ListOrganizationsQuery) ([]query.OrganizationView, error)
}

type joinCodeGenerator interface {
	Handle(ctx context.Context, cmd command.GenerateJoinCodeCommand) (command.GenerateJoinCodeResult, error)
}

type joinCodeGetter interface {
	Handle(ctx context.Context, q query.GetJoinCodeQuery) (query.JoinCodeView, error)
}

type joinCodeRevoker interface {
	Handle(ctx context.Context, cmd command.RevokeJoinCodeCommand) error
}

// Server implements orakov1connect.OrakoServiceHandler.
//
//nolint:dupl // the struct's fields necessarily mirror NewServer's params; not real duplication.
type Server struct {
	listExperts              listExpertser
	ask                      conversationAsker
	getConversation          getConversationer
	followUp                 followUpper
	resolveConversation      resolveConversationer
	dismissConversation      dismisser
	listProjects             projectsLister
	renameProject            projectRenamer
	setProjectArchived       projectArchiveSetter
	deleteProject            projectDeleter
	deleteConversation       conversationDeleter
	listProjectsDetailed     projectsDetailedLister
	listConversations        conversationsLister
	searchHistory            historySearcher
	historyCounts            historyCounter
	listKnowledge            knowledgeLister
	createKnowledge          knowledgeCreator
	updateKnowledge          knowledgeUpdater
	markKnowledgeStale       knowledgeStaleMarker
	revalidateKnowledge      knowledgeRevalidator
	listPendingKnowledge     pendingKnowledgeLister
	approveKnowledge         knowledgeApprover
	dismissKnowledge         knowledgeDismisser
	promoteConversation      conversationPromoter
	dashboardMetrics         dashboardMetricser
	listInbox                inboxLister
	getMember                memberGetter
	updateMember             memberUpdater
	listMembers              membersLister
	getOrgMember             orgMemberGetter
	setMemberAvailability    memberAvailabilitySetter
	setMemberActivation      memberActivationSetter
	setOrgAdmin              orgAdminSetter
	listConnectedChannels    connectedChannelsLister
	providerCredentials      providerCredentialsLoader
	getProviderAlertChannels providerAlertChannelsGetter
	listMcpConnections       mcpConnectionLister
	revokeMcpConnection      mcpConnectionRevoker
	heartbeat                heartbeater
	createOrganization       organizationCreator
	createProject            projectCreator
	addMember                memberAdder
	inviteMembers            membersInviter
	assignRole               roleAssigner
	setOwnExpertise          ownExpertiseSetter
	removeMember             memberRemover
	configureProvider        providerConfigurer
	syncChatBindings         chatDirectorySyncer
	disconnectProvider       providerDisconnecter
	sendProviderTest         providerTester
	getOrgSettings           orgSettingsGetter
	updateOrgSettings        orgSettingsUpdater
	renameOrganization       organizationRenamer
	deleteOrganization       organizationDeleter
	getOrganization          organizationGetter
	listOrganizations        organizationsLister
	generateJoinCode         joinCodeGenerator
	getJoinCode              joinCodeGetter
	revokeJoinCode           joinCodeRevoker
	createMachineToken       machineTokenCreator
	listMachineTokens        machineTokenViewLister
	revokeMachineToken       machineTokenRevokerHandler
	projects                 projectOrgResolver
	convScope                conversationScopeReader
	logger                   *slog.Logger
}

// NewServer constructs a Server from the given application handlers.
// All handler fields must be non-nil; nil handlers panic at construction time.
//
//nolint:dupl,funlen // params necessarily mirror the Server struct fields; a single construction site is clearer than splitting it.
func NewServer(
	listExperts listExpertser,
	ask conversationAsker,
	getConversation getConversationer,
	followUp followUpper,
	resolveConversation resolveConversationer,
	dismissConversation dismisser,
	listProjects projectsLister,
	renameProject projectRenamer,
	setProjectArchived projectArchiveSetter,
	deleteProject projectDeleter,
	deleteConversation conversationDeleter,
	listProjectsDetailed projectsDetailedLister,
	listConversations conversationsLister,
	searchHistory historySearcher,
	historyCounts historyCounter,
	listKnowledge knowledgeLister,
	createKnowledge knowledgeCreator,
	updateKnowledge knowledgeUpdater,
	markKnowledgeStale knowledgeStaleMarker,
	revalidateKnowledge knowledgeRevalidator,
	listPendingKnowledge pendingKnowledgeLister,
	approveKnowledge knowledgeApprover,
	dismissKnowledge knowledgeDismisser,
	promoteConversation conversationPromoter,
	dashboardMetrics dashboardMetricser,
	listInbox inboxLister,
	getMember memberGetter,
	updateMember memberUpdater,
	listMembers membersLister,
	getOrgMember orgMemberGetter,
	setMemberAvailability memberAvailabilitySetter,
	setMemberActivation memberActivationSetter,
	setOrgAdmin orgAdminSetter,
	listConnectedChannels connectedChannelsLister,
	providerCredentials providerCredentialsLoader,
	getProviderAlertChannels providerAlertChannelsGetter,
	listMcpConnections mcpConnectionLister,
	revokeMcpConnection mcpConnectionRevoker,
	heartbeat heartbeater,
	createOrganization organizationCreator,
	createProject projectCreator,
	addMember memberAdder,
	inviteMembers membersInviter,
	assignRole roleAssigner,
	setOwnExpertise ownExpertiseSetter,
	removeMember memberRemover,
	configureProvider providerConfigurer,
	syncChatBindings chatDirectorySyncer,
	disconnectProvider providerDisconnecter,
	sendProviderTest providerTester,
	getOrgSettings orgSettingsGetter,
	updateOrgSettings orgSettingsUpdater,
	renameOrganization organizationRenamer,
	deleteOrganization organizationDeleter,
	getOrganization organizationGetter,
	listOrganizations organizationsLister,
	generateJoinCode joinCodeGenerator,
	getJoinCode joinCodeGetter,
	revokeJoinCode joinCodeRevoker,
	createMachineToken machineTokenCreator,
	listMachineTokens machineTokenViewLister,
	revokeMachineToken machineTokenRevokerHandler,
	projects projectOrgResolver,
	convScope conversationScopeReader,
	logger *slog.Logger,
) *Server {
	return &Server{
		listExperts:              listExperts,
		ask:                      ask,
		getConversation:          getConversation,
		followUp:                 followUp,
		resolveConversation:      resolveConversation,
		dismissConversation:      dismissConversation,
		listProjects:             listProjects,
		renameProject:            renameProject,
		setProjectArchived:       setProjectArchived,
		deleteProject:            deleteProject,
		deleteConversation:       deleteConversation,
		listProjectsDetailed:     listProjectsDetailed,
		listConversations:        listConversations,
		searchHistory:            searchHistory,
		historyCounts:            historyCounts,
		listKnowledge:            listKnowledge,
		createKnowledge:          createKnowledge,
		updateKnowledge:          updateKnowledge,
		markKnowledgeStale:       markKnowledgeStale,
		revalidateKnowledge:      revalidateKnowledge,
		listPendingKnowledge:     listPendingKnowledge,
		approveKnowledge:         approveKnowledge,
		dismissKnowledge:         dismissKnowledge,
		promoteConversation:      promoteConversation,
		dashboardMetrics:         dashboardMetrics,
		listInbox:                listInbox,
		getMember:                getMember,
		updateMember:             updateMember,
		listMembers:              listMembers,
		getOrgMember:             getOrgMember,
		setMemberAvailability:    setMemberAvailability,
		setMemberActivation:      setMemberActivation,
		setOrgAdmin:              setOrgAdmin,
		listConnectedChannels:    listConnectedChannels,
		providerCredentials:      providerCredentials,
		getProviderAlertChannels: getProviderAlertChannels,
		listMcpConnections:       listMcpConnections,
		revokeMcpConnection:      revokeMcpConnection,
		heartbeat:                heartbeat,
		createOrganization:       createOrganization,
		createProject:            createProject,
		addMember:                addMember,
		inviteMembers:            inviteMembers,
		assignRole:               assignRole,
		setOwnExpertise:          setOwnExpertise,
		removeMember:             removeMember,
		configureProvider:        configureProvider,
		syncChatBindings:         syncChatBindings,
		disconnectProvider:       disconnectProvider,
		sendProviderTest:         sendProviderTest,
		getOrgSettings:           getOrgSettings,
		updateOrgSettings:        updateOrgSettings,
		renameOrganization:       renameOrganization,
		deleteOrganization:       deleteOrganization,
		getOrganization:          getOrganization,
		listOrganizations:        listOrganizations,
		generateJoinCode:         generateJoinCode,
		getJoinCode:              getJoinCode,
		revokeJoinCode:           revokeJoinCode,
		createMachineToken:       createMachineToken,
		listMachineTokens:        listMachineTokens,
		revokeMachineToken:       revokeMachineToken,
		projects:                 projects,
		convScope:                convScope,
		logger:                   logger,
	}
}

// Handler returns the HTTP path prefix and handler for mounting on a ServeMux.
// The interceptors slice is applied to every RPC in the service.
func (s *Server) Handler(interceptors ...connect.HandlerOption) (string, http.Handler) {
	return orakov1connect.NewOrakoServiceHandler(s, interceptors...)
}

// ListExperts returns the experts for a project with their domains and
// presence hints. Caller must be a member of the project.
func (s *Server) ListExperts(
	ctx context.Context,
	req *connect.Request[orakov1.ListExpertsRequest],
) (*connect.Response[orakov1.ListExpertsResponse], error) {
	projectID, err := s.resolveProject(ctx, req.Msg.GetProjectId(), false)
	if err != nil {
		return nil, err
	}

	experts, err := s.listExperts.Handle(ctx, query.ListExpertsQuery{
		ProjectID: projectID,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	pbExperts := make([]*orakov1.Expert, len(experts))

	for i, sp := range experts {
		pbExperts[i] = &orakov1.Expert{
			MemberId:    sp.MemberID.String(),
			DisplayName: sp.DisplayName,
			Domains:     sp.Domains,
			Online:      sp.Online,
			Email:       sp.Email,
			Status:      string(sp.Status),
			InvitedAt:   timestamppb.New(sp.InvitedAt),
		}
	}

	return connect.NewResponse(&orakov1.ListExpertsResponse{Experts: pbExperts}), nil
}

// Ask opens a new conversation and delivers the question to the
// responder via the configured provider. The asker identity comes from ctx.
func (s *Server) Ask(
	ctx context.Context,
	req *connect.Request[orakov1.AskRequest],
) (*connect.Response[orakov1.AskResponse], error) {
	projectID, err := s.resolveProject(ctx, req.Msg.GetProjectId(), true)
	if err != nil {
		return nil, err
	}

	// member_id and domains are one-of: an empty id means a pool
	// dispatch by domains. The exactly-one validation lives in the handler.
	targetID := uuid.Nil

	if req.Msg.GetMemberId() != "" {
		targetID, err = parseUUID(req.Msg.GetMemberId(), "member_id")
		if err != nil {
			return nil, err
		}
	}

	caller, _ := CallerFromContext(ctx)

	result, err := s.ask.Handle(ctx, command.AskCommand{
		ProjectID:         projectID,
		AskerMemberID:     caller.MemberID,
		ResponderMemberID: targetID,
		Domains:           req.Msg.GetDomains(),
		Question:          req.Msg.GetQuestion(),
		Title:             req.Msg.GetTitle(),
		Context:           req.Msg.GetContext(),
		Wait:              req.Msg.GetWait(),
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.AskResponse{
		ConversationId: result.ConversationID.String(),
		InlineAnswer:   result.InlineAnswer,
		Answered:       result.Answered,
		PoolSize:       int32(result.PoolSize), //nolint:gosec // pool size is bounded by project membership.
		ProjectName:    result.ProjectName,
		RecipientNames: result.RecipientNames,
	}), nil
}

// GetConversation fetches a conversation and all its messages (park-and-resume).
// Visibility is enforced in the query handler: only the asker, the assigned
// responder, or an org admin may read it.
func (s *Server) GetConversation(
	ctx context.Context,
	req *connect.Request[orakov1.GetConversationRequest],
) (*connect.Response[orakov1.GetConversationResponse], error) {
	convID, err := parseUUID(req.Msg.GetConversationId(), "conversation_id")
	if err != nil {
		return nil, err
	}

	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	// Org-scope the read: requireConversationAccess resolves the conversation's
	// project and admits only its project members or an admin OF THAT project's
	// org. Without this, the query handler's IsOrgAdmin bypass would let an admin
	// of ANY org read this conversation by UUID (cross-tenant read). Mirrors the
	// ResolveConversation / DismissConversation gate.
	if err := s.requireConversationAccess(ctx, convID); err != nil {
		return nil, err
	}

	view, err := s.getConversation.Handle(ctx, query.GetConversationQuery{
		ConversationID:   convID,
		CallerMemberID:   caller.MemberID,
		IsOrgAdmin:       caller.IsOrgAdmin,
		CallerProjectIDs: caller.ProjectIDs,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	pbMsgs := make([]*orakov1.ConversationMessage, len(view.Messages))

	for i, m := range view.Messages {
		pbMsgs[i] = &orakov1.ConversationMessage{
			MessageId:      m.ID.String(),
			AuthorMemberId: m.AuthorMemberID.String(),
			Role:           domainMessageRoleToProto(m.Role),
			Body:           m.Body,
			At:             timestamppb.New(m.CreatedAt),
			Source:         string(m.Source),
			AgentClient:    m.AgentClient,
		}
	}

	return connect.NewResponse(&orakov1.GetConversationResponse{
		ConversationId: view.ID.String(),
		Status:         string(view.Status),
		Messages:       pbMsgs,
		Title:          view.Title,
		Participants:   pbParticipants(view.Participants),
	}), nil
}

// pbParticipants maps read-model participants to the wire shape.
func pbParticipants(in []query.Participant) []*orakov1.Participant {
	if len(in) == 0 {
		return nil
	}

	out := make([]*orakov1.Participant, len(in))
	for i, p := range in {
		out[i] = &orakov1.Participant{
			MemberId:    p.MemberID.String(),
			DisplayName: p.DisplayName,
			Role:        p.Role,
		}
	}

	return out
}

// FollowUp appends a follow-up message to an open conversation. Author = caller.
//
//nolint:dupl // intentionally parallel to RenameProject/SetProjectArchived; merging would obscure the wire surface
func (s *Server) FollowUp(
	ctx context.Context,
	req *connect.Request[orakov1.FollowUpRequest],
) (*connect.Response[orakov1.FollowUpResponse], error) {
	convID, err := parseUUID(req.Msg.GetConversationId(), "conversation_id")
	if err != nil {
		return nil, err
	}

	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	// The result's Outcome is not yet surfaced on this RPC (phase 4 wires the
	// ack copy); the append/capture already fully happened by the time Handle
	// returns.
	if _, err = s.followUp.Handle(ctx, command.FollowUpCommand{
		ConversationID: convID,
		AuthorMemberID: caller.MemberID,
		Message:        req.Msg.GetBody(),
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.FollowUpResponse{}), nil
}

// GetOrganizationSettings returns the caller org's effective escalation
// settings (org-admin gated in the query handler).
func (s *Server) GetOrganizationSettings(
	ctx context.Context,
	_ *connect.Request[orakov1.GetOrganizationSettingsRequest],
) (*connect.Response[orakov1.GetOrganizationSettingsResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	view, err := s.getOrgSettings.Handle(ctx, query.GetOrganizationSettingsQuery{
		OrgID:      caller.OrgID,
		IsOrgAdmin: caller.IsOrgAdmin,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.GetOrganizationSettingsResponse{
		Settings: orgSettingsToProto(view),
	}), nil
}

// UpdateOrganizationSettings stores explicit escalation timeouts for the
// caller's org (org-admin gated in the command handler).
func (s *Server) UpdateOrganizationSettings(
	ctx context.Context,
	req *connect.Request[orakov1.UpdateOrganizationSettingsRequest],
) (*connect.Response[orakov1.UpdateOrganizationSettingsResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if err := s.updateOrgSettings.Handle(ctx, command.UpdateOrganizationSettingsCommand{
		OrgID:               caller.OrgID,
		IsOrgAdmin:          caller.IsOrgAdmin,
		ClaimTimeoutSeconds: req.Msg.GetClaimTimeoutSeconds(),
		// AlertTimeoutSeconds is optional int64: the field itself (not the
		// zero-defaulting getter) carries presence through to the command, so
		// an absent field leaves the stored value unchanged instead of
		// disabling alerts.
		AlertTimeoutSeconds:   req.Msg.AlertTimeoutSeconds,
		DefaultAlertChannelID: req.Msg.GetDefaultAlertChannelId(),
	}); err != nil {
		return nil, toConnectError(err)
	}

	// Echo the now-effective settings so the client needs no second read.
	view, err := s.getOrgSettings.Handle(ctx, query.GetOrganizationSettingsQuery{
		OrgID:      caller.OrgID,
		IsOrgAdmin: caller.IsOrgAdmin,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.UpdateOrganizationSettingsResponse{
		Settings: orgSettingsToProto(view),
	}), nil
}

// RenameOrganization renames the caller's organization (org-admin gated by the
// interceptor). The org id is resolved from the caller identity, never from the
// request body; the handler validates and trims the new name.
func (s *Server) RenameOrganization(
	ctx context.Context,
	req *connect.Request[orakov1.RenameOrganizationRequest],
) (*connect.Response[orakov1.RenameOrganizationResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	result, err := s.renameOrganization.Handle(ctx, command.RenameOrganizationCommand{
		OrgID: caller.OrgID,
		Name:  req.Msg.GetName(),
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.RenameOrganizationResponse{Name: result.Name}), nil
}

// DeleteOrganization hard-deletes the caller's organization and all its data
// (org-admin only; interceptor-gated). The org is resolved from the caller
// identity, never the request; confirm_name must match the org's current name.
// Irreversible.
func (s *Server) DeleteOrganization(
	ctx context.Context,
	req *connect.Request[orakov1.DeleteOrganizationRequest],
) (*connect.Response[orakov1.DeleteOrganizationResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if _, err := s.deleteOrganization.Handle(ctx, command.DeleteOrganizationCommand{
		OrgID:       caller.OrgID,
		ConfirmName: req.Msg.GetConfirmName(),
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.DeleteOrganizationResponse{}), nil
}

// GetOrganization returns the caller's organization identity (id + name) so
// the dashboard can render the real org name and slug.
func (s *Server) GetOrganization(
	ctx context.Context,
	_ *connect.Request[orakov1.GetOrganizationRequest],
) (*connect.Response[orakov1.GetOrganizationResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	view, err := s.getOrganization.Handle(ctx, query.GetOrganizationQuery{OrgID: caller.OrgID})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.GetOrganizationResponse{
		OrganizationId: view.ID.String(),
		Name:           view.Name,
	}), nil
}

// ListOrganizations returns every organization the caller participates in, with
// the active one flagged. The dashboard uses it for the org switcher.
func (s *Server) ListOrganizations(
	ctx context.Context,
	_ *connect.Request[orakov1.ListOrganizationsRequest],
) (*connect.Response[orakov1.ListOrganizationsResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	views, err := s.listOrganizations.Handle(ctx, query.ListOrganizationsQuery{
		AccountID:    caller.AccountID,
		MemberID:     caller.MemberID,
		CurrentOrgID: caller.OrgID,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	orgs := make([]*orakov1.OrganizationSummary, len(views))
	for i, v := range views {
		orgs[i] = &orakov1.OrganizationSummary{
			Id:        v.ID.String(),
			Name:      v.Name,
			IsCurrent: v.IsCurrent,
		}
	}

	return connect.NewResponse(&orakov1.ListOrganizationsResponse{Organizations: orgs}), nil
}

// GenerateJoinCode rotates the caller org's shareable join code (org-admin only;
// interceptor-gated). The org comes from the caller identity (never the request
// body); an explicit project_id is confirmed to belong to that org, an empty one
// resolves to the org's default project. Generating revokes any prior live code.
func (s *Server) GenerateJoinCode(
	ctx context.Context,
	req *connect.Request[orakov1.GenerateJoinCodeRequest],
) (*connect.Response[orakov1.GenerateJoinCodeResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	var projectID uuid.UUID

	if raw := req.Msg.GetProjectId(); raw != "" {
		parsed, err := parseUUID(raw, "project_id")
		if err != nil {
			return nil, err
		}

		projectID = parsed
	}

	result, err := s.generateJoinCode.Handle(ctx, command.GenerateJoinCodeCommand{
		OrgID:         caller.OrgID,
		ProjectID:     projectID,
		ActorMemberID: caller.MemberID,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.GenerateJoinCodeResponse{
		Code:        result.Code,
		ProjectId:   result.ProjectID.String(),
		ProjectName: result.ProjectName,
	}), nil
}

// GetJoinCode returns the caller org's current live join code, or an inactive
// result when none exists (org-admin only; interceptor-gated). Org from identity.
func (s *Server) GetJoinCode(
	ctx context.Context,
	_ *connect.Request[orakov1.GetJoinCodeRequest],
) (*connect.Response[orakov1.GetJoinCodeResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	view, err := s.getJoinCode.Handle(ctx, query.GetJoinCodeQuery{OrgID: caller.OrgID})
	if err != nil {
		return nil, toConnectError(err)
	}

	resp := &orakov1.GetJoinCodeResponse{
		Code:        view.Code,
		ProjectName: view.ProjectName,
		Active:      view.Active,
	}
	if view.Active {
		resp.ProjectId = view.ProjectID.String()
	}

	return connect.NewResponse(resp), nil
}

// RevokeJoinCode revokes the caller org's live join code (org-admin only;
// interceptor-gated). Idempotent. Org from identity.
func (s *Server) RevokeJoinCode(
	ctx context.Context,
	_ *connect.Request[orakov1.RevokeJoinCodeRequest],
) (*connect.Response[orakov1.RevokeJoinCodeResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if err := s.revokeJoinCode.Handle(ctx, command.RevokeJoinCodeCommand{OrgID: caller.OrgID}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.RevokeJoinCodeResponse{}), nil
}

// orgSettingsToProto maps the settings view to its wire form.
func orgSettingsToProto(view query.OrgSettingsView) *orakov1.OrganizationSettings {
	return &orakov1.OrganizationSettings{
		ClaimTimeoutSeconds:   view.ClaimTimeoutSeconds,
		ClaimTimeoutIsDefault: view.ClaimIsDefault,
		AlertTimeoutSeconds:   view.AlertTimeoutSeconds,
		AlertTimeoutIsDefault: view.AlertIsDefault,
		DefaultAlertChannelId: view.DefaultAlertChannelID,
	}
}

// ResolveConversation closes a conversation and records the resolution on it.
//
//nolint:dupl // intentionally parallel to ConfigureProvider; merging would obscure the wire surface.
func (s *Server) ResolveConversation(
	ctx context.Context,
	req *connect.Request[orakov1.ResolveConversationRequest],
) (*connect.Response[orakov1.ResolveConversationResponse], error) {
	convID, err := parseUUID(req.Msg.GetConversationId(), "conversation_id")
	if err != nil {
		return nil, err
	}

	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if err = s.requireConversationAccess(ctx, convID); err != nil {
		return nil, err
	}

	if _, err := s.resolveConversation.Handle(ctx, command.ResolveConversationCommand{
		ConversationID: convID,
		CloserMemberID: caller.MemberID,
		Resolution:     req.Msg.GetResolution(),
		Contested:      req.Msg.GetContested(),
		ContestedSet:   req.Msg.GetContestedSet(),
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.ResolveConversationResponse{}), nil
}

// DismissConversation closes an unanswered conversation without recording a
// resolution. Not admin-gated (dismissing a dead thread is normal member action),
// so requireConversationAccess enforces that the caller belongs to the
// conversation's project before it can act by bare id.
func (s *Server) DismissConversation(
	ctx context.Context,
	req *connect.Request[orakov1.DismissConversationRequest],
) (*connect.Response[orakov1.DismissConversationResponse], error) {
	convID, err := parseUUID(req.Msg.GetConversationId(), "conversation_id")
	if err != nil {
		return nil, err
	}

	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if err = s.requireConversationAccess(ctx, convID); err != nil {
		return nil, err
	}

	result, err := s.dismissConversation.Handle(ctx, command.DismissConversationCommand{
		ConversationID:    convID,
		DismisserMemberID: caller.MemberID,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.DismissConversationResponse{
		Status: string(result.Status),
	}), nil
}

// ListProjects returns the projects the caller is a member of (any role).
// RBAC: any authenticated caller; the project list is scoped to the caller's
// MemberID from the auth context — no per-project check is applied here.
func (s *Server) ListProjects(
	ctx context.Context,
	req *connect.Request[orakov1.ListProjectsRequest],
) (*connect.Response[orakov1.ListProjectsResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	// all_orgs (consent screens) drops the active-org filter so a member can
	// see every project across every org they belong to and scope a token
	// cross-org.
	orgFilter := caller.OrgID
	if req.Msg.GetAllOrgs() {
		orgFilter = uuid.Nil
	}

	summaries, err := s.listProjects.Handle(ctx, query.ListProjectsQuery{
		CallerMemberID: caller.MemberID,
		OrgID:          orgFilter,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	pbProjects := make([]*orakov1.ProjectSummary, 0, len(summaries))

	for _, p := range summaries {
		// Scope discovery to the connection's reachable projects: a scoped
		// token only lists what it can act on.
		if !slices.Contains(caller.ProjectIDs, p.ID) {
			continue
		}

		pbProjects = append(pbProjects, &orakov1.ProjectSummary{
			Id:    p.ID.String(),
			Name:  p.Name,
			Role:  domainRoleToProto(p.Role),
			OrgId: p.OrgID.String(),
		})
	}

	return connect.NewResponse(&orakov1.ListProjectsResponse{Projects: pbProjects}), nil
}

// ListConversations returns conversations in a project, newest first.
// RBAC: the caller's token project must match project_id (same as SearchHistory).
// Visibility: a non-admin caller sees only conversations they asked or are the
// assigned responder for; an org admin sees every conversation in the project.
func (s *Server) ListConversations(
	ctx context.Context,
	req *connect.Request[orakov1.ListConversationsRequest],
) (*connect.Response[orakov1.ListConversationsResponse], error) {
	orgID, projectIDs, err := s.resolveProjectScope(ctx, req.Msg.GetProjectId(), req.Msg.GetProjectIds())
	if err != nil {
		return nil, err
	}

	caller, _ := CallerFromContext(ctx) // presence guaranteed by the interceptor

	summaries, err := s.listConversations.Handle(ctx, query.ListConversationsQuery{
		ProjectIDs:     projectIDs,
		OrgID:          orgID,
		Status:         req.Msg.GetStatus(),
		CallerMemberID: caller.MemberID,
		IsOrgAdmin:     caller.IsOrgAdmin,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	pbConvs := make([]*orakov1.ConversationSummary, len(summaries))

	for i, c := range summaries {
		pbConvs[i] = &orakov1.ConversationSummary{
			Id:                c.ID.String(),
			Status:            string(c.Status),
			Question:          c.Question,
			Title:             c.Title,
			ResponderMemberId: c.ResponderMemberID.String(),
			CreatedAt:         timestamppb.New(c.CreatedAt),
			ProjectId:         c.ProjectID.String(),
			ProjectName:       c.ProjectName,
			Participants:      pbParticipants(c.Participants),
		}
	}

	return connect.NewResponse(&orakov1.ListConversationsResponse{Conversations: pbConvs}), nil
}

// searchHistoryTopK is the default hit cap for the dashboard History tab (higher
// than the agent-facing MCP default so the browse list and status facets stay
// well-populated).
const searchHistoryTopK = 50

// SearchHistory backs the dashboard History tab: it runs the deterministic
// (embedding-free) history search over the caller's scope and, alongside the
// ranked hits, returns the per-status conversation breakdown for the KPI and
// facet pills. Scope mirrors ListConversations (org + optional project_ids,
// archived excluded); an empty query browses the most recent conversations.
// The search engine is org/project-scoped (a shared knowledge base), not
// narrowed per-member — the same design as the MCP search_history tool.
func (s *Server) SearchHistory(
	ctx context.Context,
	req *connect.Request[orakov1.SearchHistoryRequest],
) (*connect.Response[orakov1.SearchHistoryResponse], error) {
	orgID, projectIDs, err := s.resolveProjectScope(ctx, "", req.Msg.GetProjectIds())
	if err != nil {
		return nil, err
	}

	topK := int(req.Msg.GetTopK())
	if topK <= 0 {
		topK = searchHistoryTopK
	}

	hits, err := s.searchHistory.Handle(ctx, query.SearchHistoryQuery{
		OrgID:      orgID,
		ProjectIDs: projectIDs,
		Query:      req.Msg.GetQuery(),
		Tags:       req.Msg.GetTags(),
		Status:     req.Msg.GetStatus(),
		TopK:       topK,
		Browse:     true,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	// Counts always cover the whole scope (never the status filter), so the KPI
	// and facet pill numbers are exact even when the card list is status-filtered.
	counts, err := s.historyCounts.Handle(ctx, query.HistoryStatusCountsQuery{
		OrgID:      orgID,
		ProjectIDs: projectIDs,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	pbHits := make([]*orakov1.HistoryHit, len(hits))

	for i, h := range hits {
		pbHits[i] = &orakov1.HistoryHit{
			ConversationId: h.ConversationID.String(),
			ProjectId:      h.ProjectID.String(),
			ProjectName:    h.ProjectName,
			Title:          h.Title,
			Summary:        h.Summary,
			Status:         string(h.Status),
			Tags:           h.Tags,
			Entities:       h.Entities,
			AskerMemberId:  h.AskerMemberID.String(),
			CreatedAt:      timestamppb.New(h.CreatedAt),
			AgeDays:        int32(h.AgeDays), //nolint:gosec // age in days is small and non-negative.
			Score:          float64(h.Score),
		}
	}

	return connect.NewResponse(&orakov1.SearchHistoryResponse{
		Hits:  pbHits,
		Total: int32(counts.All), //nolint:gosec // conversation count is bounded.
		Counts: &orakov1.HistoryStatusCounts{
			All:       int32(counts.All),       //nolint:gosec // bounded conversation count.
			Resolved:  int32(counts.Resolved),  //nolint:gosec // bounded conversation count.
			Answered:  int32(counts.Answered),  //nolint:gosec // bounded conversation count.
			Open:      int32(counts.Open),      //nolint:gosec // bounded conversation count.
			TimedOut:  int32(counts.TimedOut),  //nolint:gosec // bounded conversation count.
			Dismissed: int32(counts.Dismissed), //nolint:gosec // bounded conversation count.
		},
	}), nil
}

// ListKnowledgeEntries returns the org's curated knowledge entries (every
// status) for the dashboard Knowledge section. Org-scoped — any member may read
// the team's curated answers (not admin-gated). Scope mirrors SearchHistory.
//
//nolint:dupl // parallel to ListPendingKnowledge but a distinct RPC surface; merging would obscure the two read shapes.
func (s *Server) ListKnowledgeEntries(
	ctx context.Context,
	req *connect.Request[orakov1.ListKnowledgeEntriesRequest],
) (*connect.Response[orakov1.ListKnowledgeEntriesResponse], error) {
	orgID, projectIDs, err := s.resolveProjectScope(ctx, "", req.Msg.GetProjectIds())
	if err != nil {
		return nil, err
	}

	views, err := s.listKnowledge.Handle(ctx, query.ListKnowledgeEntriesQuery{
		OrgID:      orgID,
		ProjectIDs: projectIDs,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	entries := make([]*orakov1.KnowledgeEntry, len(views))
	for i, v := range views {
		entries[i] = pbKnowledgeEntry(v)
	}

	return connect.NewResponse(&orakov1.ListKnowledgeEntriesResponse{Entries: entries}), nil
}

// pbKnowledgeEntry maps a read-model KnowledgeEntryView to the wire shape. A
// uuid.Nil project/author maps to an empty string (org-wide / unattributed).
func pbKnowledgeEntry(v query.KnowledgeEntryView) *orakov1.KnowledgeEntry {
	return &orakov1.KnowledgeEntry{
		Id:             v.ID.String(),
		ProjectId:      uuidToStringOrEmpty(v.ProjectID),
		ProjectName:    v.ProjectName,
		Question:       v.Question,
		Answer:         v.Answer,
		Summary:        v.Summary,
		Tags:           v.Tags,
		Entities:       v.Entities,
		AuthorMemberId: uuidToStringOrEmpty(v.AuthorMemberID),
		Source:         v.Source,
		Status:         v.Status,
		CreatedAt:      timestamppb.New(v.CreatedAt),
		UpdatedAt:      timestamppb.New(v.UpdatedAt),
	}
}

// uuidToStringOrEmpty renders id, or "" for uuid.Nil, so an absent optional id
// serializes as an empty string rather than the all-zero UUID.
func uuidToStringOrEmpty(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}

	return id.String()
}

// CreateKnowledgeEntry authors one curated entry (org-admin only;
// interceptor-gated). Org + author come from the token; an optional project_id
// scopes the entry (empty = org-wide) and must belong to the caller's org.
func (s *Server) CreateKnowledgeEntry(
	ctx context.Context,
	req *connect.Request[orakov1.CreateKnowledgeEntryRequest],
) (*connect.Response[orakov1.CreateKnowledgeEntryResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	projectID, err := s.resolveKnowledgeProject(ctx, req.Msg.GetProjectId(), caller.OrgID)
	if err != nil {
		return nil, err
	}

	result, err := s.createKnowledge.Handle(ctx, command.CreateKnowledgeEntryCommand{
		OrgID:          caller.OrgID,
		ProjectID:      projectID,
		AuthorMemberID: caller.MemberID,
		Question:       req.Msg.GetQuestion(),
		Answer:         req.Msg.GetAnswer(),
		Summary:        req.Msg.GetSummary(),
		Tags:           req.Msg.GetTags(),
		Entities:       req.Msg.GetEntities(),
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.CreateKnowledgeEntryResponse{EntryId: result.EntryID.String()}), nil
}

// resolveKnowledgeProject parses an optional project_id for a curated entry and
// verifies it belongs to orgID. An empty project_id resolves to uuid.Nil
// (org-wide). A project in another org is reported not-found (no existence leak).
func (s *Server) resolveKnowledgeProject(ctx context.Context, raw string, orgID uuid.UUID) (uuid.UUID, error) {
	if strings.TrimSpace(raw) == "" {
		return uuid.Nil, nil
	}

	projectID, err := parseUUID(raw, "project_id")
	if err != nil {
		return uuid.Nil, err
	}

	project, err := s.projects.ByID(ctx, projectID)
	if err != nil || project.OrgID != orgID {
		return uuid.Nil, connect.NewError(connect.CodeNotFound, errors.New("project not found in this organization"))
	}

	return projectID, nil
}

// UpdateKnowledgeEntry rewrites an entry's editable fields (org-admin only;
// interceptor-gated). OrgID from the token scopes the target to the caller's org.
func (s *Server) UpdateKnowledgeEntry(
	ctx context.Context,
	req *connect.Request[orakov1.UpdateKnowledgeEntryRequest],
) (*connect.Response[orakov1.UpdateKnowledgeEntryResponse], error) {
	entryID, err := parseUUID(req.Msg.GetEntryId(), "entry_id")
	if err != nil {
		return nil, err
	}

	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if _, err = s.updateKnowledge.Handle(ctx, command.UpdateKnowledgeEntryCommand{
		EntryID:  entryID,
		OrgID:    caller.OrgID,
		Question: req.Msg.GetQuestion(),
		Answer:   req.Msg.GetAnswer(),
		Summary:  req.Msg.GetSummary(),
		Tags:     req.Msg.GetTags(),
		Entities: req.Msg.GetEntities(),
		Status:   req.Msg.GetStatus(),
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.UpdateKnowledgeEntryResponse{}), nil
}

// MarkKnowledgeStale flags an entry stale so it drops out of search (org-admin
// only; interceptor-gated). Reversible via UpdateKnowledgeEntry status="active".
//
//nolint:dupl // intentionally parallel to the other single-id admin handlers; merging would obscure the wire surface
func (s *Server) MarkKnowledgeStale(
	ctx context.Context,
	req *connect.Request[orakov1.MarkKnowledgeStaleRequest],
) (*connect.Response[orakov1.MarkKnowledgeStaleResponse], error) {
	entryID, err := parseUUID(req.Msg.GetEntryId(), "entry_id")
	if err != nil {
		return nil, err
	}

	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if _, err = s.markKnowledgeStale.Handle(ctx, command.MarkKnowledgeStaleCommand{
		EntryID: entryID,
		OrgID:   caller.OrgID,
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.MarkKnowledgeStaleResponse{}), nil
}

// RevalidateKnowledgeEntry re-confirms a stale entry as current (status back to
// active + updated_at bumped). Org-admin only; interceptor-gated.
//
//nolint:dupl // intentionally parallel to the other single-id admin handlers; merging would obscure the wire surface
func (s *Server) RevalidateKnowledgeEntry(
	ctx context.Context,
	req *connect.Request[orakov1.RevalidateKnowledgeEntryRequest],
) (*connect.Response[orakov1.RevalidateKnowledgeEntryResponse], error) {
	entryID, err := parseUUID(req.Msg.GetEntryId(), "entry_id")
	if err != nil {
		return nil, err
	}

	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if _, err = s.revalidateKnowledge.Handle(ctx, command.RevalidateKnowledgeEntryCommand{
		EntryID: entryID,
		OrgID:   caller.OrgID,
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.RevalidateKnowledgeEntryResponse{}), nil
}

// ListPendingKnowledge returns the org's agent-suggested entries awaiting review
// (source=agent_suggested, status=pending), newest first, for the dashboard's
// "Pending review" queue. Org-admin only; interceptor-gated. Scope mirrors
// ListKnowledgeEntries.
//
//nolint:dupl // parallel to ListKnowledgeEntries but a distinct RPC surface; merging would obscure the two read shapes.
func (s *Server) ListPendingKnowledge(
	ctx context.Context,
	req *connect.Request[orakov1.ListPendingKnowledgeRequest],
) (*connect.Response[orakov1.ListPendingKnowledgeResponse], error) {
	orgID, projectIDs, err := s.resolveProjectScope(ctx, "", req.Msg.GetProjectIds())
	if err != nil {
		return nil, err
	}

	views, err := s.listPendingKnowledge.Handle(ctx, query.ListPendingKnowledgeEntriesQuery{
		OrgID:      orgID,
		ProjectIDs: projectIDs,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	entries := make([]*orakov1.KnowledgeEntry, len(views))
	for i, v := range views {
		entries[i] = pbKnowledgeEntry(v)
	}

	return connect.NewResponse(&orakov1.ListPendingKnowledgeResponse{Entries: entries}), nil
}

// ApproveKnowledgeEntry approves a pending agent-suggested entry (status →
// active; source kept as agent_suggested for provenance). Org-admin only;
// interceptor-gated.
//
//nolint:dupl // intentionally parallel to the other single-id admin handlers; merging would obscure the wire surface
func (s *Server) ApproveKnowledgeEntry(
	ctx context.Context,
	req *connect.Request[orakov1.ApproveKnowledgeEntryRequest],
) (*connect.Response[orakov1.ApproveKnowledgeEntryResponse], error) {
	entryID, err := parseUUID(req.Msg.GetEntryId(), "entry_id")
	if err != nil {
		return nil, err
	}

	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if _, err = s.approveKnowledge.Handle(ctx, command.ApproveKnowledgeEntryCommand{
		EntryID: entryID,
		OrgID:   caller.OrgID,
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.ApproveKnowledgeEntryResponse{}), nil
}

// DismissKnowledgeEntry rejects a pending agent-suggested entry by deleting the
// row. Org-admin only; interceptor-gated.
//
//nolint:dupl // intentionally parallel to the other single-id admin handlers; merging would obscure the wire surface
func (s *Server) DismissKnowledgeEntry(
	ctx context.Context,
	req *connect.Request[orakov1.DismissKnowledgeEntryRequest],
) (*connect.Response[orakov1.DismissKnowledgeEntryResponse], error) {
	entryID, err := parseUUID(req.Msg.GetEntryId(), "entry_id")
	if err != nil {
		return nil, err
	}

	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if _, err = s.dismissKnowledge.Handle(ctx, command.DismissKnowledgeEntryCommand{
		EntryID: entryID,
		OrgID:   caller.OrgID,
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.DismissKnowledgeEntryResponse{}), nil
}

// PromoteConversationToKnowledge distills a resolved conversation into a curated
// entry authored by the promoter. Org-admin only; interceptor-gated. The
// conversation must belong to a project in the caller's org (enforced by the
// command reading the conversation's project against the caller OrgID scope).
func (s *Server) PromoteConversationToKnowledge(
	ctx context.Context,
	req *connect.Request[orakov1.PromoteConversationToKnowledgeRequest],
) (*connect.Response[orakov1.PromoteConversationToKnowledgeResponse], error) {
	convID, err := parseUUID(req.Msg.GetConversationId(), "conversation_id")
	if err != nil {
		return nil, err
	}

	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	// Cross-tenant guard: the conversation's project must be reachable by the
	// caller (an org admin of another tenant must not promote a conversation by
	// UUID). requireProjectMember inside also honors the org-admin bypass.
	if err := s.requireConversationAccess(ctx, convID); err != nil {
		return nil, err
	}

	result, err := s.promoteConversation.Handle(ctx, command.PromoteConversationToKnowledgeCommand{
		ConversationID:   convID,
		OrgID:            caller.OrgID,
		PromoterMemberID: caller.MemberID,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.PromoteConversationToKnowledgeResponse{EntryId: result.EntryID.String()}), nil
}

// GetDashboardMetrics returns the org KPI overview for the caller's scope over a
// period. Scope mirrors ListConversations/SearchHistory (org + optional
// project_ids, archived excluded). The metrics are org/project-scoped (a shared
// team view), not narrowed per-member.
func (s *Server) GetDashboardMetrics(
	ctx context.Context,
	req *connect.Request[orakov1.GetDashboardMetricsRequest],
) (*connect.Response[orakov1.GetDashboardMetricsResponse], error) {
	orgID, projectIDs, err := s.resolveProjectScope(ctx, "", req.Msg.GetProjectIds())
	if err != nil {
		return nil, err
	}

	m, err := s.dashboardMetrics.Handle(ctx, query.DashboardMetricsQuery{
		OrgID:      orgID,
		ProjectIDs: projectIDs,
		Period:     req.Msg.GetPeriod(),
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.GetDashboardMetricsResponse{
		Conversations:              pbMetricCard(m.Conversations),
		ResponseRate:               pbMetricCard(m.ResponseRate),
		MedianFirstResponseSeconds: pbMetricCard(m.MedianFirstResponseSecond),
		Reuse: &orakov1.ReuseCard{
			Available: m.Reuse.Available,
			Pct:       m.Reuse.Pct,
			DeltaPts:  m.Reuse.DeltaPts,
		},
		OpenCount:        m.OpenCount,
		LeadsCount:       m.LeadsCount,
		ToHandle:         pbConversationRefs(m.ToHandle),
		RecentlyResolved: pbHistoryRefs(m.RecentlyResolved),
		TopResponders:    pbResponders(m.TopResponders),
		TopTopics:        pbTopics(m.TopTopics),
	}), nil
}

// pbMetricCard maps a read-model MetricCard to the wire shape.
func pbMetricCard(c query.MetricCard) *orakov1.MetricCard {
	return &orakov1.MetricCard{
		Value:    c.Value,
		DeltaPct: c.DeltaPct,
		Series:   c.Series,
	}
}

// pbConversationRefs maps the to-handle list to the wire shape.
func pbConversationRefs(in []query.DashboardConversationRef) []*orakov1.ConversationRef {
	out := make([]*orakov1.ConversationRef, len(in))
	for i, r := range in {
		out[i] = &orakov1.ConversationRef{
			ConversationId: r.ConversationID.String(),
			ProjectId:      r.ProjectID.String(),
			Title:          r.Title,
			Status:         r.Status,
			AskerMemberId:  r.AskerMemberID.String(),
			CreatedAt:      timestamppb.New(r.CreatedAt),
		}
	}

	return out
}

// pbHistoryRefs maps the recently-resolved list to the wire shape.
func pbHistoryRefs(in []query.DashboardHistoryRef) []*orakov1.HistoryRef {
	out := make([]*orakov1.HistoryRef, len(in))
	for i, r := range in {
		resolver := ""
		if r.ResolverMemberID != uuid.Nil {
			resolver = r.ResolverMemberID.String()
		}

		out[i] = &orakov1.HistoryRef{
			ConversationId:   r.ConversationID.String(),
			Title:            r.Title,
			Tags:             r.Tags,
			ResolverMemberId: resolver,
			ResolvedAt:       timestamppb.New(r.ResolvedAt),
		}
	}

	return out
}

// pbResponders maps the top-responders leaderboard to the wire shape.
func pbResponders(in []query.DashboardResponder) []*orakov1.Responder {
	out := make([]*orakov1.Responder, len(in))
	for i, r := range in {
		out[i] = &orakov1.Responder{
			MemberId:    r.MemberID.String(),
			DisplayName: r.DisplayName,
			AnswerCount: r.AnswerCount,
		}
	}

	return out
}

// pbTopics maps the top-topics leaderboard to the wire shape.
func pbTopics(in []query.DashboardTopic) []*orakov1.TopicCount {
	out := make([]*orakov1.TopicCount, len(in))
	for i, t := range in {
		out[i] = &orakov1.TopicCount{
			Label: t.Label,
			Count: t.Count,
		}
	}

	return out
}

// ListInbox returns the caller's pending questions (open conversations where the
// caller is the responder). RBAC: any authenticated caller; scoped to their own
// MemberID from the auth context.
func (s *Server) ListInbox(
	ctx context.Context,
	_ *connect.Request[orakov1.ListInboxRequest],
) (*connect.Response[orakov1.ListInboxResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	items, err := s.listInbox.Handle(ctx, query.ListInboxQuery{
		ResponderMemberID: caller.MemberID,
		OrgID:             caller.OrgID,
		IsOrgAdmin:        caller.IsOrgAdmin,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	pbItems := make([]*orakov1.InboxItem, len(items))

	for i, it := range items {
		pbItems[i] = &orakov1.InboxItem{
			ConversationId: it.ConversationID.String(),
			ProjectId:      it.ProjectID.String(),
			Question:       it.Question,
			Title:          it.Title,
			AskerMemberId:  it.AskerMemberID.String(),
			Status:         string(it.Status),
			CreatedAt:      timestamppb.New(it.CreatedAt),
		}
	}

	return connect.NewResponse(&orakov1.ListInboxResponse{Items: pbItems}), nil
}

// GetMember returns the caller's own contact settings. Caller = token.
func (s *Server) GetMember(
	ctx context.Context,
	_ *connect.Request[orakov1.GetMemberRequest],
) (*connect.Response[orakov1.GetMemberResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	view, err := s.getMember.Handle(ctx, query.GetMemberQuery{MemberID: caller.MemberID})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.GetMemberResponse{
		Member:     pbMember(view),
		IsOrgAdmin: caller.IsOrgAdmin,
	}), nil
}

// UpdateMember updates a member's contact settings. Caller = token; a member
// edits themselves by default. A non-empty member_id targets another member
// and is org-admin gated (checked inside the command handler, since
// UpdateMember is not one of the interceptor's adminProcedures — the RPC
// serves both the self-service and the admin-fills-a-card paths).
func (s *Server) UpdateMember(
	ctx context.Context,
	req *connect.Request[orakov1.UpdateMemberRequest],
) (*connect.Response[orakov1.UpdateMemberResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	var target uuid.UUID

	if raw := req.Msg.GetMemberId(); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid member_id"))
		}

		target = parsed
	}

	updated, err := s.updateMember.Handle(ctx, command.UpdateMemberCommand{
		MemberID:        caller.MemberID,
		TargetMemberID:  target,
		IsOrgAdmin:      caller.IsOrgAdmin,
		OrgID:           caller.OrgID,
		DisplayName:     req.Msg.GetDisplayName(),
		DeliveryChannel: model.DeliveryChannel(req.Msg.GetDeliveryChannel()),
		SlackUserID:     req.Msg.GetSlackUserId(),
		TelegramChatID:  req.Msg.GetTelegramChatId(),
		TeamsUserID:     req.Msg.GetTeamsUserId(),
		DiscordUserID:   req.Msg.GetDiscordUserId(),
		Email:           req.Msg.GetEmail(),
		FirstName:       req.Msg.GetFirstName(),
		LastName:        req.Msg.GetLastName(),
		GitHandle:       req.Msg.GetGitHandle(),
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.UpdateMemberResponse{Member: pbMember(query.NewMemberView(updated))}), nil
}

// ListMcpConnections returns the caller's connected MCP clients (one entry
// per grant_id), member-scoped — not an admin RPC.
func (s *Server) ListMcpConnections(
	ctx context.Context,
	_ *connect.Request[orakov1.ListMcpConnectionsRequest],
) (*connect.Response[orakov1.ListMcpConnectionsResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	grants, err := s.listMcpConnections.ListGrantsByMember(ctx, caller.MemberID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	out := make([]*orakov1.McpConnection, 0, len(grants))
	for _, g := range grants {
		out = append(out, pbMcpConnection(g))
	}

	return connect.NewResponse(&orakov1.ListMcpConnectionsResponse{Connections: out}), nil
}

// RevokeMcpConnection disconnects one of the caller's MCP clients
// immediately, member-scoped — not an admin RPC. The store filters by
// member_id, so a caller can never revoke another member's connection.
func (s *Server) RevokeMcpConnection(
	ctx context.Context,
	req *connect.Request[orakov1.RevokeMcpConnectionRequest],
) (*connect.Response[orakov1.RevokeMcpConnectionResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	grantID, err := parseUUID(req.Msg.GetGrantId(), "grant_id")
	if err != nil {
		return nil, err
	}

	if err := s.revokeMcpConnection.RevokeGrantForMember(ctx, caller.MemberID, grantID); err != nil {
		if errors.Is(err, adaptererr.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}

		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&orakov1.RevokeMcpConnectionResponse{}), nil
}

// pbMcpConnection maps an oauth.Grant to its wire view (never the tokens).
func pbMcpConnection(g oauth.Grant) *orakov1.McpConnection {
	pb := &orakov1.McpConnection{
		GrantId:     g.GrantID.String(),
		ClientName:  g.ClientName,
		Resource:    g.Resource,
		ProjectIds:  make([]string, len(g.ProjectIDs)),
		ConnectedAt: timestamppb.New(g.ConnectedAt),
		ExpiresAt:   timestamppb.New(g.ExpiresAt),
	}

	for i, id := range g.ProjectIDs {
		pb.ProjectIds[i] = id.String()
	}

	if g.LastUsedAt != nil {
		pb.LastUsedAt = timestamppb.New(*g.LastUsedAt)
	}

	return pb
}

// parseProjectIDs parses a repeated project_ids field into UUIDs, naming the
// field in the error for an actionable CodeInvalidArgument.
func parseProjectIDs(raw []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(raw))

	for _, s := range raw {
		id, err := parseUUID(s, "project_ids")
		if err != nil {
			return nil, err
		}

		out = append(out, id)
	}

	return out, nil
}

// ListConnectedChannels returns the delivery channels the caller may select for
// their primary project. Caller = token.
func (s *Server) ListConnectedChannels(
	ctx context.Context,
	_ *connect.Request[orakov1.ListConnectedChannelsRequest],
) (*connect.Response[orakov1.ListConnectedChannelsResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	channels, err := s.listConnectedChannels.Handle(ctx, query.ListConnectedChannelsQuery{ProjectID: caller.ProjectID})
	if err != nil {
		return nil, toConnectError(err)
	}

	alertChannels, err := s.getProviderAlertChannels.Handle(ctx, query.GetProviderAlertChannelsQuery{ProjectID: caller.ProjectID})
	if err != nil {
		return nil, toConnectError(err)
	}

	connected := make([]*orakov1.ConnectedChannel, len(alertChannels))
	for i, ac := range alertChannels {
		connected[i] = &orakov1.ConnectedChannel{
			Kind:            ac.Kind,
			AlertChannelIds: ac.AlertChannelIDs,
			BotClientId:     s.botClientID(ctx, caller.ProjectID, ac.Kind),
		}
	}

	return connect.NewResponse(&orakov1.ListConnectedChannelsResponse{
		Channels:  channels,
		Connected: connected,
	}), nil
}

// botClientID derives the Discord bot's PUBLIC application id from the stored
// bot token (its first dot-segment is the base64url-encoded app id). Only the
// id crosses the wire — never the token. Best-effort: empty on any failure or
// for non-Discord kinds.
func (s *Server) botClientID(ctx context.Context, projectID uuid.UUID, kind string) string {
	if kind != "discord" || s.providerCredentials == nil {
		return ""
	}

	raw, err := s.providerCredentials.LoadProvider(ctx, projectID, kind)
	if err != nil {
		return ""
	}

	var creds struct {
		BotToken string `json:"bot_token"`
	}

	if err := json.Unmarshal(raw, &creds); err != nil || creds.BotToken == "" {
		return ""
	}

	seg, _, _ := strings.Cut(creds.BotToken, ".")

	decoded, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		// Some tokens pad the segment; retry with standard padding-tolerant decode.
		if decoded, err = base64.URLEncoding.DecodeString(seg); err != nil {
			return ""
		}
	}

	id := string(decoded)
	if len(id) < 17 || len(id) > 20 {
		return ""
	}

	for _, r := range id {
		if r < '0' || r > '9' {
			return ""
		}
	}

	return id
}

// pbMember maps a MemberView DTO to the proto Member message.
func pbMember(v query.MemberView) *orakov1.Member {
	return &orakov1.Member{
		MemberId:        v.ID.String(),
		Email:           v.Email,
		DisplayName:     v.DisplayName,
		DeliveryChannel: string(v.DeliveryChannel),
		SlackUserId:     v.SlackUserID,
		TelegramChatId:  v.TelegramChatID,
		Status:          string(v.Status),
		FirstName:       v.FirstName,
		LastName:        v.LastName,
		GitHandle:       v.GitHandle,
		TeamsUserId:     v.TeamsUserID,
		DiscordUserId:   v.DiscordUserID,
		BindingError:    v.BindingError,
	}
}

// Heartbeat refreshes the caller's connection-presence.
func (s *Server) Heartbeat(
	ctx context.Context,
	_ *connect.Request[orakov1.HeartbeatRequest],
) (*connect.Response[orakov1.HeartbeatResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if err := s.heartbeat.Handle(ctx, command.HeartbeatCommand{
		MemberID:  caller.MemberID,
		ProjectID: caller.ProjectID,
		Online:    true,
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.HeartbeatResponse{}), nil
}

// Admin RPCs below are gated by the RBAC interceptor before reaching these
// handlers; the handlers themselves do not re-check role.

// CreateOrganization creates an organization with its global project and makes
// the caller the org admin. Identity comes from the token (AccountID); no
// project RBAC is required because the caller has no org/member yet.
func (s *Server) CreateOrganization(
	ctx context.Context,
	req *connect.Request[orakov1.CreateOrganizationRequest],
) (*connect.Response[orakov1.CreateOrganizationResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok || caller.AccountID == uuid.Nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authenticated account required"))
	}

	if req.Msg.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name must not be empty"))
	}

	result, err := s.createOrganization.Handle(ctx, command.CreateOrganizationCommand{
		Name:             req.Msg.GetName(),
		CreatorAccountID: caller.AccountID,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.CreateOrganizationResponse{
		OrganizationId:  result.OrgID.String(),
		GlobalProjectId: result.GlobalProjectID.String(),
	}), nil
}

// CreateProject creates a new Orako project. Creator = caller.
func (s *Server) CreateProject(
	ctx context.Context,
	req *connect.Request[orakov1.CreateProjectRequest],
) (*connect.Response[orakov1.CreateProjectResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	result, err := s.createProject.Handle(ctx, command.CreateProjectCommand{
		Name:            req.Msg.GetName(),
		OrgID:           caller.OrgID,
		CreatorMemberID: caller.MemberID,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.CreateProjectResponse{
		ProjectId: result.ProjectID.String(),
	}), nil
}

// RenameProject renames a project (org-admin only; interceptor-gated).
//
//nolint:dupl // intentionally parallel to SetProjectArchived/DeleteProject/FollowUp; merging would obscure the wire surface
func (s *Server) RenameProject(
	ctx context.Context,
	req *connect.Request[orakov1.RenameProjectRequest],
) (*connect.Response[orakov1.RenameProjectResponse], error) {
	projectID, err := parseUUID(req.Msg.GetProjectId(), "project_id")
	if err != nil {
		return nil, err
	}

	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if _, err = s.renameProject.Handle(ctx, command.RenameProjectCommand{
		ProjectID: projectID,
		OrgID:     caller.OrgID,
		Name:      req.Msg.GetName(),
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.RenameProjectResponse{}), nil
}

// SetProjectArchived freezes or reactivates a project (org-admin only;
// interceptor-gated).
//
//nolint:dupl // intentionally parallel to RenameProject/DeleteProject/FollowUp; merging would obscure the wire surface
func (s *Server) SetProjectArchived(
	ctx context.Context,
	req *connect.Request[orakov1.SetProjectArchivedRequest],
) (*connect.Response[orakov1.SetProjectArchivedResponse], error) {
	projectID, err := parseUUID(req.Msg.GetProjectId(), "project_id")
	if err != nil {
		return nil, err
	}

	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if _, err = s.setProjectArchived.Handle(ctx, command.SetProjectArchivedCommand{
		ProjectID: projectID,
		OrgID:     caller.OrgID,
		Archived:  req.Msg.GetArchived(),
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.SetProjectArchivedResponse{}), nil
}

// DeleteProject hard-deletes a project and every dependent row (org-admin
// only; interceptor-gated). Irreversible.
//
//nolint:dupl // intentionally parallel to DeleteConversation; merging would obscure the wire surface
func (s *Server) DeleteProject(
	ctx context.Context,
	req *connect.Request[orakov1.DeleteProjectRequest],
) (*connect.Response[orakov1.DeleteProjectResponse], error) {
	projectID, err := parseUUID(req.Msg.GetProjectId(), "project_id")
	if err != nil {
		return nil, err
	}

	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if _, err = s.deleteProject.Handle(ctx, command.DeleteProjectCommand{
		ProjectID: projectID,
		OrgID:     caller.OrgID,
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.DeleteProjectResponse{}), nil
}

// DeleteConversation hard-deletes a conversation and everything derived from it
// (org-admin only; interceptor-gated). OrgID scopes the target to the caller's
// own org. Irreversible.
//
//nolint:dupl // intentionally parallel to DeleteProject; merging would obscure the wire surface
func (s *Server) DeleteConversation(
	ctx context.Context,
	req *connect.Request[orakov1.DeleteConversationRequest],
) (*connect.Response[orakov1.DeleteConversationResponse], error) {
	convID, err := parseUUID(req.Msg.GetConversationId(), "conversation_id")
	if err != nil {
		return nil, err
	}

	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if _, err = s.deleteConversation.Handle(ctx, command.DeleteConversationCommand{
		ConversationID: convID,
		OrgID:          caller.OrgID,
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.DeleteConversationResponse{}), nil
}

// ListProjectsDetailed backs the Projects tab: identity, archived status,
// aggregate stats, and per-provider alert routing for every project in the
// caller's org (org-admin only; interceptor-gated).
func (s *Server) ListProjectsDetailed(
	ctx context.Context,
	req *connect.Request[orakov1.ListProjectsDetailedRequest],
) (*connect.Response[orakov1.ListProjectsDetailedResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	details, err := s.listProjectsDetailed.Handle(ctx, query.ListProjectsDetailedQuery{
		OrgID:           caller.OrgID,
		IncludeArchived: req.Msg.GetIncludeArchived(),
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	pb := make([]*orakov1.ProjectDetail, len(details))
	for i, d := range details {
		pb[i] = pbProjectDetail(d)
	}

	return connect.NewResponse(&orakov1.ListProjectsDetailedResponse{Projects: pb}), nil
}

// pbProjectDetail maps a ProjectDetail DTO to its wire form.
func pbProjectDetail(d query.ProjectDetail) *orakov1.ProjectDetail {
	routing := make([]*orakov1.ProviderAlertRouting, len(d.Routing))
	for i, r := range d.Routing {
		routing[i] = &orakov1.ProviderAlertRouting{
			Kind:            r.Kind,
			AlertChannelIds: r.AlertChannelIDs,
		}
	}

	return &orakov1.ProjectDetail{
		ProjectId:         d.ID.String(),
		Name:              d.Name,
		Archived:          d.Archived,
		MemberCount:       int32(d.MemberCount),       //nolint:gosec // project member counts are small positive integers.
		ConversationCount: int32(d.ConversationCount), //nolint:gosec // project conversation counts are small positive integers.
		ResolvedCount:     int32(d.ResolvedCount),     //nolint:gosec // project resolved counts are small positive integers.
		Routing:           routing,
	}
}

// AddMember adds a member to a project.
func (s *Server) AddMember(
	ctx context.Context,
	req *connect.Request[orakov1.AddMemberRequest],
) (*connect.Response[orakov1.AddMemberResponse], error) {
	projectID, err := parseUUID(req.Msg.GetProjectId(), "project_id")
	if err != nil {
		return nil, err
	}

	if err = s.requireProjectInCallerOrg(ctx, projectID); err != nil {
		return nil, err
	}

	result, err := s.addMember.Handle(ctx, command.AddMemberCommand{
		ProjectID:      projectID,
		Email:          req.Msg.GetEmail(),
		DisplayName:    req.Msg.GetEmail(), // proto has no display_name; email is the fallback
		SlackUserID:    req.Msg.GetSlackUserId(),
		TelegramChatID: req.Msg.GetTelegramChatId(),
		Domains:        req.Msg.GetDomains(),
		Resend:         req.Msg.GetResend(),
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.AddMemberResponse{
		MemberId: result.MemberID.String(),
	}), nil
}

// InviteMembers invites a list of emails to a project in one call (org-admin,
// interceptor-gated; project-in-org scoped). Reports per-email status.
func (s *Server) InviteMembers(
	ctx context.Context,
	req *connect.Request[orakov1.InviteMembersRequest],
) (*connect.Response[orakov1.InviteMembersResponse], error) {
	projectID, err := parseUUID(req.Msg.GetProjectId(), "project_id")
	if err != nil {
		return nil, err
	}

	if err = s.requireProjectInCallerOrg(ctx, projectID); err != nil {
		return nil, err
	}

	result, err := s.inviteMembers.Handle(ctx, command.InviteMembersCommand{
		ProjectID: projectID,
		Emails:    req.Msg.GetEmails(),
		Domains:   req.Msg.GetDomains(),
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	out := make([]*orakov1.InviteOutcome, 0, len(result.Results))
	for _, r := range result.Results {
		out = append(out, &orakov1.InviteOutcome{Email: r.Email, Status: r.Status})
	}

	return connect.NewResponse(&orakov1.InviteMembersResponse{Results: out}), nil
}

// parseProjectAndMember parses the (project_id, member_id) pair shared by the
// member-administration RPCs.
func parseProjectAndMember(projectID, memberID string) (uuid.UUID, uuid.UUID, error) {
	pid, err := parseUUID(projectID, "project_id")
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}

	mid, err := parseUUID(memberID, "member_id")
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}

	return pid, mid, nil
}

// AssignRole sets a member's expertise tags (domains) within a project. The RPC
// name is retained for wire stability; the permission-role concept is retired.
func (s *Server) AssignRole(
	ctx context.Context,
	req *connect.Request[orakov1.AssignRoleRequest],
) (*connect.Response[orakov1.AssignRoleResponse], error) {
	projectID, memberID, err := parseProjectAndMember(req.Msg.GetProjectId(), req.Msg.GetMemberId())
	if err != nil {
		return nil, err
	}

	if err = s.requireProjectInCallerOrg(ctx, projectID); err != nil {
		return nil, err
	}

	if err = s.assignRole.Handle(ctx, command.AssignRoleCommand{
		ProjectID: projectID,
		MemberID:  memberID,
		Domains:   req.Msg.GetDomains(),
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.AssignRoleResponse{}), nil
}

// SetOwnExpertise sets the CALLER's expertise tags across all their projects.
// Self-scoped (not admin-gated): the member is derived ONLY from the
// authenticated caller identity (identity invariant — never from a request
// field), so a plain member can set their own expertise during onboarding. A
// caller with no member id (uuid.Nil) is rejected: they have joined no project
// yet and have nothing to set.
func (s *Server) SetOwnExpertise(
	ctx context.Context,
	req *connect.Request[orakov1.SetOwnExpertiseRequest],
) (*connect.Response[orakov1.SetOwnExpertiseResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if caller.MemberID == uuid.Nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("caller is not a project member yet"))
	}

	if err := s.setOwnExpertise.Handle(ctx, caller.MemberID, req.Msg.GetDomains()); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.SetOwnExpertiseResponse{}), nil
}

// RemoveMember archives (default) or purges PII for a member.
func (s *Server) RemoveMember(
	ctx context.Context,
	req *connect.Request[orakov1.RemoveMemberRequest],
) (*connect.Response[orakov1.RemoveMemberResponse], error) {
	projectID, memberID, err := parseProjectAndMember(req.Msg.GetProjectId(), req.Msg.GetMemberId())
	if err != nil {
		return nil, err
	}

	if err = s.requireMemberInCallerOrg(ctx, memberID); err != nil {
		return nil, err
	}

	if err = s.requireProjectInCallerOrg(ctx, projectID); err != nil {
		return nil, err
	}

	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if err = s.removeMember.Handle(ctx, command.RemoveMemberCommand{
		MemberID:  memberID,
		OrgID:     caller.OrgID,
		ProjectID: projectID,
		Purge:     req.Msg.GetPurgePii(),
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.RemoveMemberResponse{}), nil
}

// ListMembers returns the org roster (scoped to caller.OrgID). Not admin-gated:
// a member must be able to see the team to route questions to it. A NON-admin
// caller gets the REDACTED roster (identity + per-project expertise only) via
// pbOrgMemberRoster — email, external chat IDs, binding errors, availability and
// the org-admin flag stay admin-only. An admin gets the full record AND the
// pending self-join members (Invited tier) appended for approval; a non-admin
// never sees any pending member. Roster mutations and single-member detail
// (GetOrgMember) stay admin-only (see interceptor.go's adminProcedures).
func (s *Server) ListMembers(
	ctx context.Context,
	_ *connect.Request[orakov1.ListMembersRequest],
) (*connect.Response[orakov1.ListMembersResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	// Pending self-join members (join-code redeemers awaiting approval) are
	// hidden from the roster everyone sees; only an org admin gets them appended,
	// so a non-admin caller can never read a pending member through this RPC.
	members, err := s.listMembers.Handle(ctx, query.ListMembersQuery{
		OrgID:          caller.OrgID,
		IncludePending: caller.IsOrgAdmin,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	pb := make([]*orakov1.OrgMember, len(members))
	for i, m := range members {
		if caller.IsOrgAdmin {
			pb[i] = pbOrgMember(m)
		} else {
			pb[i] = pbOrgMemberRoster(m)
		}
	}

	return connect.NewResponse(&orakov1.ListMembersResponse{Members: pb}), nil
}

// GetOrgMember returns one member's rich record (org-admin only).
func (s *Server) GetOrgMember(
	ctx context.Context,
	req *connect.Request[orakov1.GetOrgMemberRequest],
) (*connect.Response[orakov1.GetOrgMemberResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	memberID, err := parseUUID(req.Msg.GetMemberId(), "member_id")
	if err != nil {
		return nil, err
	}

	m, err := s.getOrgMember.Handle(ctx, query.GetOrgMemberQuery{OrgID: caller.OrgID, MemberID: memberID})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.GetOrgMemberResponse{Member: pbOrgMember(m)}), nil
}

// SetMemberAvailability flips a member between active and on_leave (org-admin only).
func (s *Server) SetMemberAvailability(
	ctx context.Context,
	req *connect.Request[orakov1.SetMemberAvailabilityRequest],
) (*connect.Response[orakov1.SetMemberAvailabilityResponse], error) {
	memberID, err := parseUUID(req.Msg.GetMemberId(), "member_id")
	if err != nil {
		return nil, err
	}

	var returnDate *time.Time

	if rd := req.Msg.GetReturnDate(); rd != "" {
		t, perr := time.Parse("2006-01-02", rd)
		if perr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("return_date must be YYYY-MM-DD: %w", perr))
		}

		returnDate = &t
	}

	if err := s.requireMemberInCallerOrg(ctx, memberID); err != nil {
		return nil, err
	}

	if err := s.setMemberAvailability.Handle(ctx, command.SetMemberAvailabilityCommand{
		MemberID:   memberID,
		Status:     model.MemberStatus(req.Msg.GetStatus()),
		ReturnDate: returnDate,
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.SetMemberAvailabilityResponse{}), nil
}

// SetMemberActivation deactivates or reactivates a member (org-admin only).
func (s *Server) SetMemberActivation(
	ctx context.Context,
	req *connect.Request[orakov1.SetMemberActivationRequest],
) (*connect.Response[orakov1.SetMemberActivationResponse], error) {
	memberID, err := parseUUID(req.Msg.GetMemberId(), "member_id")
	if err != nil {
		return nil, err
	}

	if err := s.requireMemberInCallerOrg(ctx, memberID); err != nil {
		return nil, err
	}

	if err := s.setMemberActivation.Handle(ctx, command.SetMemberActivationCommand{
		MemberID: memberID,
		Active:   req.Msg.GetActive(),
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.SetMemberActivationResponse{}), nil
}

// SetOrgAdmin grants or revokes org-admin on another member (org-admin only).
func (s *Server) SetOrgAdmin(
	ctx context.Context,
	req *connect.Request[orakov1.SetOrgAdminRequest],
) (*connect.Response[orakov1.SetOrgAdminResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	memberID, err := parseUUID(req.Msg.GetMemberId(), "member_id")
	if err != nil {
		return nil, err
	}

	// Confirm the target belongs to the caller's org before flipping their admin
	// bit — same guard the other member RPCs use, so a foreign-org member can't
	// be targeted.
	if err := s.requireMemberInCallerOrg(ctx, memberID); err != nil {
		return nil, err
	}

	if err := s.setOrgAdmin.Handle(ctx, command.SetOrgAdminCommand{
		OrgID:    caller.OrgID,
		MemberID: memberID,
		IsAdmin:  req.Msg.GetIsAdmin(),
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.SetOrgAdminResponse{}), nil
}

// pbOrgMember maps the rich domain view to the wire OrgMember.
func pbOrgMember(m query.OrgMemberView) *orakov1.OrgMember {
	projects := make([]*orakov1.ProjectExpertise, len(m.Projects))
	for i, p := range m.Projects {
		projects[i] = &orakov1.ProjectExpertise{ProjectId: p.ProjectID.String(), Domains: p.Domains}
	}

	returnDate := ""
	if m.ReturnDate != nil {
		returnDate = m.ReturnDate.Format("2006-01-02")
	}

	return &orakov1.OrgMember{
		MemberId:        m.MemberID.String(),
		Email:           m.Email,
		FirstName:       m.FirstName,
		LastName:        m.LastName,
		DisplayName:     m.DisplayName,
		GitHandle:       m.GitHandle,
		DeliveryChannel: m.DeliveryChannel,
		SlackUserId:     m.SlackUserID,
		TeamsUserId:     m.TeamsUserID,
		TelegramChatId:  m.TelegramChatID,
		DiscordUserId:   m.DiscordUserID,
		BindingError:    m.BindingError,
		Status:          m.Status,
		ReturnDate:      returnDate,
		IsOrgAdmin:      m.IsOrgAdmin,
		External:        m.External,
		Projects:        projects,
	}
}

// pbOrgMemberRoster is the REDACTED projection returned to a non-admin caller of
// ListMembers: the team roster they need to route questions — identity, git
// handle, status, membership tier (org-admin flag) and per-project expertise —
// with every sensitive field (email, external chat IDs, delivery channel,
// binding error, availability / return date) deliberately zeroed. The org-admin
// flag is NOT redacted: it carries the Admin/Team tier the Team page shows to
// every member (who the org admins are is not a secret, unlike PII). Kept as an
// explicit full literal so the redaction is auditable and a newly added
// OrgMember field is a compile-time decision, not a silent leak.
func pbOrgMemberRoster(m query.OrgMemberView) *orakov1.OrgMember {
	projects := make([]*orakov1.ProjectExpertise, len(m.Projects))
	for i, p := range m.Projects {
		projects[i] = &orakov1.ProjectExpertise{ProjectId: p.ProjectID.String(), Domains: p.Domains}
	}

	return &orakov1.OrgMember{
		MemberId:    m.MemberID.String(),
		FirstName:   m.FirstName,
		LastName:    m.LastName,
		DisplayName: m.DisplayName,
		GitHandle:   m.GitHandle,
		Status:      m.Status,
		External:    m.External,
		Projects:    projects,
		// Membership tier (Admin/Team) — shown to every member, not redacted.
		IsOrgAdmin: m.IsOrgAdmin,
		// Redacted for non-admins (admin-only via GetOrgMember):
		Email:           "",
		DeliveryChannel: "",
		SlackUserId:     "",
		TeamsUserId:     "",
		TelegramChatId:  "",
		DiscordUserId:   "",
		BindingError:    "",
		ReturnDate:      "",
	}
}

// SyncChatDirectory backfills Slack ids for a project's members by email.
// Org-admin (interceptor-gated) and project-in-org scoped; best-effort.
func (s *Server) SyncChatDirectory(
	ctx context.Context,
	req *connect.Request[orakov1.SyncChatDirectoryRequest],
) (*connect.Response[orakov1.SyncChatDirectoryResponse], error) {
	projectID, err := parseUUID(req.Msg.GetProjectId(), "project_id")
	if err != nil {
		return nil, err
	}

	if err := s.requireProjectInCallerOrg(ctx, projectID); err != nil {
		return nil, err
	}

	res, err := s.syncChatBindings.Handle(ctx, command.SyncChatBindingsCommand{ProjectID: projectID})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.SyncChatDirectoryResponse{
		Scanned: int32(res.Scanned), //nolint:gosec // member counts are bounded by org size
		Bound:   int32(res.Bound),   //nolint:gosec // subset of scanned
	}), nil
}

// ConfigureProvider sets the messaging provider for a project (admin-only; RBAC
// gate is applied by the interceptor before this handler is called).
//
//nolint:dupl // intentionally parallel to ResolveConversation; merging would obscure the wire surface.
func (s *Server) ConfigureProvider(
	ctx context.Context,
	req *connect.Request[orakov1.ConfigureProviderRequest],
) (*connect.Response[orakov1.ConfigureProviderResponse], error) {
	projectID, err := parseUUID(req.Msg.GetProjectId(), "project_id")
	if err != nil {
		return nil, err
	}

	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	// Credentials are keyed to the caller's org, but the project_id also drives
	// alert-channel rows and the live provider registry, so confirm it is in the
	// caller's org before configuring anything against it.
	if err := s.requireProjectInCallerOrg(ctx, projectID); err != nil {
		return nil, err
	}

	if _, err = s.configureProvider.Handle(ctx, command.ConfigureProviderCommand{
		ProjectID:       projectID,
		OrgID:           caller.OrgID,
		Kind:            req.Msg.GetKind(),
		Credentials:     req.Msg.GetCredentials(),
		AlertChannelIDs: req.Msg.GetAlertChannelIds(),
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.ConfigureProviderResponse{}), nil
}

// DisconnectProvider removes a project's messaging provider (admin-only; RBAC
// gate is applied by the interceptor before this handler is called).
func (s *Server) DisconnectProvider(
	ctx context.Context,
	req *connect.Request[orakov1.DisconnectProviderRequest],
) (*connect.Response[orakov1.DisconnectProviderResponse], error) {
	projectID, err := parseUUID(req.Msg.GetProjectId(), "project_id")
	if err != nil {
		return nil, err
	}

	// Confirm the project belongs to the caller's org: without this an admin of
	// org X could disconnect org Y's provider (knocking its delivery offline) by
	// project UUID.
	if err := s.requireProjectInCallerOrg(ctx, projectID); err != nil {
		return nil, err
	}

	if _, err = s.disconnectProvider.Handle(ctx, command.DisconnectProviderCommand{
		ProjectID: projectID,
		Kind:      req.Msg.GetKind(),
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.DisconnectProviderResponse{}), nil
}

// SendProviderTest runs a real delivery test (org-admin only; interceptor-gated)
// and returns a per-target result so the admin sees exactly what works.
func (s *Server) SendProviderTest(
	ctx context.Context,
	req *connect.Request[orakov1.SendProviderTestRequest],
) (*connect.Response[orakov1.SendProviderTestResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	results, err := s.sendProviderTest.Handle(ctx, command.SendProviderTestCommand{
		ProjectID: caller.ProjectID,
		MemberID:  caller.MemberID,
		Kind:      req.Msg.GetKind(),
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	pb := make([]*orakov1.ProviderTestResult, len(results))
	for i, r := range results {
		pb[i] = &orakov1.ProviderTestResult{Label: r.Label, Ok: r.OK, Error: r.Error}
	}

	return connect.NewResponse(&orakov1.SendProviderTestResponse{Results: pb}), nil
}

// parseUUID parses a string field as a UUID, wrapping parse errors in
// CodeInvalidArgument.
func parseUUID(s, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New(field+" must be a valid UUID"))
	}

	return id, nil
}

// requireProjectMember returns CodePermissionDenied when the caller identity
// in ctx does not belong to projectID. It returns CodeUnauthenticated when no
// identity is present (interceptor was bypassed).
func (s *Server) requireProjectMember(ctx context.Context, projectID uuid.UUID) error {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if slices.Contains(caller.ProjectIDs, projectID) {
		return nil
	}

	// An org admin may reach any project in their own org, even when it is not
	// in their scope. This is strictly weaker than the cross-project writes
	// (AddMember, RemoveMember, ConfigureProvider) org admins already perform, so
	// it opens no read the admin could not already effect. Suppressed for a
	// scoped connection: a token restricted to a project subset is a hard
	// ceiling even for an admin. Non-admins stay pinned to their scope.
	if !caller.Scoped && caller.IsOrgAdmin && caller.OrgID != uuid.Nil {
		if project, err := s.projects.ByID(ctx, projectID); err == nil && project.OrgID == caller.OrgID {
			return nil
		}
	}

	return connect.NewError(connect.CodePermissionDenied,
		errors.New("caller is not a member of the requested project"))
}

// resolveProject resolves the project an agent tool acts on. An explicit
// project_id is parsed and checked with requireProjectMember (which enforces
// the token's scope); an empty project_id resolves to the caller's primary
// project. When writeGuard is set (a project-creating write like Ask),
// an omitted project_id is rejected if the connection scopes more than one
// project, so the write cannot land in the wrong one.
func (s *Server) resolveProject(ctx context.Context, raw string, writeGuard bool) (uuid.UUID, error) {
	if strings.TrimSpace(raw) == "" {
		caller, ok := CallerFromContext(ctx)
		if !ok {
			return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
		}

		if writeGuard && len(caller.ProjectIDs) > 1 {
			return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"project_id is required: this connection is scoped to %d projects — call ListProjects and pass one",
				len(caller.ProjectIDs),
			))
		}

		if caller.ProjectID == uuid.Nil {
			return uuid.Nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("no project available for this connection"))
		}

		return caller.ProjectID, nil
	}

	projectID, err := parseUUID(raw, "project_id")
	if err != nil {
		return uuid.Nil, err
	}

	if err := s.requireProjectMember(ctx, projectID); err != nil {
		return uuid.Nil, err
	}

	return projectID, nil
}

// resolveProjectScope determines the (org, project-set) scope for a
// multi-project read (ListConversations, SearchHistory). multiRaw
// (project_ids) takes precedence over the legacy singleRaw (project_id); both
// empty resolves to every non-archived project in the caller's org the caller
// may see — the org-wide read. Returns the caller's OrgID and the resolved
// project IDs: an empty ids slice paired with a non-nil OrgID means "every
// project in the org", a scope only an unscoped org admin can trigger (a
// non-admin's own visible set, caller.ProjectIDs, is never empty in practice
// once they hold any membership).
func (s *Server) resolveProjectScope(ctx context.Context, singleRaw string, multiRaw []string) (uuid.UUID, []uuid.UUID, error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return uuid.Nil, nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	if len(multiRaw) > 0 {
		ids, err := parseProjectIDs(multiRaw)
		if err != nil {
			return uuid.Nil, nil, err
		}

		// An unscoped org admin may request any project in their own org; the
		// store's org_id filter is the safety net. A non-admin (or a scoped
		// connection, even an admin's) is intersected with its own visibility.
		if caller.IsOrgAdmin && !caller.Scoped {
			return caller.OrgID, ids, nil
		}

		visible := make(map[uuid.UUID]struct{}, len(caller.ProjectIDs))
		for _, id := range caller.ProjectIDs {
			visible[id] = struct{}{}
		}

		scoped := make([]uuid.UUID, 0, len(ids))

		for _, id := range ids {
			if _, ok := visible[id]; ok {
				scoped = append(scoped, id)
			}
		}

		return caller.OrgID, scoped, nil
	}

	if strings.TrimSpace(singleRaw) != "" {
		projectID, err := s.resolveProject(ctx, singleRaw, false)
		if err != nil {
			return uuid.Nil, nil, err
		}

		return caller.OrgID, []uuid.UUID{projectID}, nil
	}

	// Both empty: an unscoped org admin gets the org-wide read (nil ids, store
	// falls back to org_id); everyone else gets their own visible project set.
	if caller.IsOrgAdmin && !caller.Scoped {
		return caller.OrgID, nil, nil
	}

	return caller.OrgID, caller.ProjectIDs, nil
}

// domainRoleToProto converts a domain model.Role to the proto Role enum.
func domainRoleToProto(r model.Role) orakov1.Role {
	switch r {
	case model.RoleUnspecified:
		return orakov1.Role_ROLE_UNSPECIFIED
	case model.RoleDev:
		return orakov1.Role_ROLE_DEV
	case model.RoleSpecialist:
		return orakov1.Role_ROLE_SPECIALIST
	case model.RoleLead:
		return orakov1.Role_ROLE_LEAD
	case model.RoleAdmin:
		return orakov1.Role_ROLE_ADMIN
	}

	return orakov1.Role_ROLE_UNSPECIFIED
}

// domainMessageRoleToProto converts a domain model.MessageRole to the proto type.
func domainMessageRoleToProto(r model.MessageRole) orakov1.MessageRole {
	switch r {
	case model.MessageRoleUnspecified:
		return orakov1.MessageRole_MESSAGE_ROLE_UNSPECIFIED
	case model.MessageRoleQuestion:
		return orakov1.MessageRole_MESSAGE_ROLE_QUESTION
	case model.MessageRoleAnswer:
		return orakov1.MessageRole_MESSAGE_ROLE_ANSWER
	case model.MessageRoleFollowUp:
		return orakov1.MessageRole_MESSAGE_ROLE_FOLLOW_UP
	case model.MessageRoleSystem:
		return orakov1.MessageRole_MESSAGE_ROLE_SYSTEM
	case model.MessageRoleSecondOpinion:
		return orakov1.MessageRole_MESSAGE_ROLE_SECOND_OPINION
	}

	return orakov1.MessageRole_MESSAGE_ROLE_UNSPECIFIED
}
