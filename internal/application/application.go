// SPDX-License-Identifier: AGPL-3.0-or-later

// Package application is the composition root for the Orako server: it wires
// driven adapters into the CQRS handlers and the event router.
package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/google/uuid"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/orako-io/core/internal/adapters/conversation"
	"github.com/orako-io/core/internal/adapters/eventlog"
	"github.com/orako-io/core/internal/adapters/identity"
	"github.com/orako-io/core/internal/adapters/integration"
	"github.com/orako-io/core/internal/adapters/knowledge"
	"github.com/orako-io/core/internal/adapters/messaging"
	"github.com/orako-io/core/internal/adapters/presence"
	"github.com/orako-io/core/internal/adapters/provider"
	"github.com/orako-io/core/internal/adapters/provider/slack"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/event"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/decorator"
	"github.com/orako-io/core/internal/pkg/edition"
	"github.com/orako-io/core/internal/pkg/metrics"
	"github.com/orako-io/core/internal/pkg/postgres"
	"github.com/orako-io/core/internal/pkg/secretbox"
)

// Commands groups every CQRS write-side handler.
type Commands struct {
	Ask                 command.AskHandler
	FollowUp            command.FollowUpHandler
	UploadAttachment    command.UploadAttachmentHandler
	AddParticipant      command.AddParticipantHandler
	ResolveConversation decorator.Handler[command.ResolveConversationCommand, command.ResolveConversationResult]
	DismissConversation decorator.Handler[command.DismissConversationCommand, command.DismissConversationResult]
	Heartbeat           command.HeartbeatHandler

	CreateOrganization command.CreateOrganizationHandler
	CreateProject      command.CreateProjectHandler
	RenameProject      command.RenameProjectHandler
	SetProjectArchived command.SetProjectArchivedHandler
	DeleteProject      command.DeleteProjectHandler
	DeleteOrganization command.DeleteOrganizationHandler
	DeleteConversation command.DeleteConversationHandler
	AddMember          command.AddMemberHandler
	AssignRole         command.AssignRoleHandler
	SetOwnExpertise    command.SetOwnExpertiseHandler
	RemoveMember       command.RemoveMemberHandler
	ConfigureProvider  command.ConfigureProviderHandler
	DisconnectProvider command.DisconnectProviderHandler
	SendProviderTest   command.SendProviderTestHandler
	UpdateMember       command.UpdateMemberHandler
	RedeemJoinToken    command.RedeemJoinTokenHandler
	GenerateJoinCode   command.GenerateJoinCodeHandler
	RevokeJoinCode     command.RevokeJoinCodeHandler

	SetMemberAvailability command.SetMemberAvailabilityHandler
	SetMemberActivation   command.SetMemberActivationHandler
	SetOrgAdmin           command.SetOrgAdminHandler
	SyncChatBindings      command.SyncChatBindingsHandler
	BindMemberChannel     command.BindMemberChannelHandler
	InviteMembers         command.InviteMembersHandler

	UpdateOrganizationSettings command.UpdateOrganizationSettingsHandler

	RenameOrganization command.RenameOrganizationHandler

	CreateKnowledgeEntry           command.CreateKnowledgeEntryHandler
	UpdateKnowledgeEntry           command.UpdateKnowledgeEntryHandler
	MarkKnowledgeStale             command.MarkKnowledgeStaleHandler
	SuggestKnowledgeEntry          command.SuggestKnowledgeEntryHandler
	ApproveKnowledgeEntry          command.ApproveKnowledgeEntryHandler
	DismissKnowledgeEntry          command.DismissKnowledgeEntryHandler
	RevalidateKnowledgeEntry       command.RevalidateKnowledgeEntryHandler
	PromoteConversationToKnowledge command.PromoteConversationToKnowledgeHandler
}

// Queries groups every CQRS read-side handler.
type Queries struct {
	SearchHistory            query.SearchHistoryHandler
	HistoryStatusCounts      query.HistoryStatusCountsHandler
	GetDashboardMetrics      query.GetDashboardMetricsHandler
	HistoryVocabulary        query.HistoryVocabularyHandler
	ListExperts              query.ListExpertsHandler
	GetConversation          decorator.Handler[query.GetConversationQuery, query.ConversationView]
	ListProjects             query.ListProjectsHandler
	ListProjectsDetailed     query.ListProjectsDetailedHandler
	ListConversations        query.ListConversationsHandler
	ListInbox                query.ListInboxHandler
	GetMember                query.GetMemberHandler
	ListMembers              query.ListMembersHandler
	GetOrgMember             query.GetOrgMemberHandler
	ListConnectedChannels    query.ListConnectedChannelsHandler
	GetProviderAlertChannels query.GetProviderAlertChannelsHandler

	GetOrganizationSettings query.GetOrganizationSettingsHandler
	GetOrganization         query.GetOrganizationHandler
	ListOrganizations       query.ListOrganizationsHandler
	GetJoinCode             query.GetJoinCodeHandler
	ListKnowledgeEntries    query.ListKnowledgeEntriesHandler
	ListPendingKnowledge    query.ListPendingKnowledgeEntriesHandler
}

// App owns the command/query handlers and event runtime.
type App struct {
	Commands Commands
	Queries  Queries

	Events     *messaging.GoChannelBus
	Escalation *event.EscalationSweeper

	router               *message.Router
	relay                *messaging.Relay
	projectAddedNotifier *event.ProjectAddedNotifier
	logger               *slog.Logger
}

type providerKindResolver struct{ reg *provider.Registry }

func (r providerKindResolver) ForProjectKind(ctx context.Context, projectID uuid.UUID, kind string) (service.Provider, error) {
	return r.reg.ForProjectKind(ctx, projectID, provider.ProviderKind(kind))
}

// New wires the application. The caller retains ownership of pool.
//
//nolint:funlen // CQRS wiring: one construction site for every handler.
func New(pool *pgxpool.Pool, reg *provider.Registry, mailer service.Mailer, inviteLinks service.InviteLinkGenerator, blobs service.BlobStore, credCipher *secretbox.Cipher, baseURL string, live *edition.Live, saasGate command.SeatGate, saasOrgHook command.OrgCreatedHook, logger *slog.Logger) (*App, error) {
	eventStore := eventlog.NewStore(pool)
	convStore := conversation.NewStore(pool).WithBlobDeleter(blobs)
	knowledgeStore := knowledge.NewStore(pool)
	txor := postgres.NewTransactor(pool)
	memberStore := identity.NewMemberStore(pool)
	projectStore := identity.NewProjectStore(pool)
	joinTokenStore := identity.NewJoinTokenStore(pool)
	presenceStore := presence.NewStore(pool)
	providerStore := integration.NewProjectProviderStore(pool, credCipher)
	candidateStore := conversation.NewCandidateStore(pool)
	escalationStore := conversation.NewEscalationStore(pool)
	providerMessageStore := integration.NewProviderMessageStore(pool)
	attachmentStore := conversation.NewAttachmentStore(pool)

	orgStore := identity.NewOrganizationStore(pool)
	openConvStore := conversation.NewOpenConversationStore(pool)

	seatGate := command.NewLiveMemberGate(pool, projectStore, live)
	if saasGate != nil {
		seatGate = saasGate
	}

	orgGate := command.NewLiveOrgGate(pool, live)
	projGate := command.NewLiveProjectGate(pool, live)

	slackDirectory := slack.NewOrgDirectory(integration.NewOrgResolvingProviderLoader(projectStore, integration.NewOrgProviderStore(pool, credCipher)))
	surfaceManager := event.NewSurfaceManager(conversation.NewSurfaceStore(pool), memberStore, convStore, providerStore, reg, attachmentStore, blobs, logger)

	outboxStore := eventlog.NewOutboxStore(pool)
	relayWake := make(chan struct{}, 1)
	bus := messaging.NewGoChannelBus(eventStore, relayWake, logger)
	relay := messaging.NewRelay(outboxStore, bus.Publisher(), relayWake, logger)

	router, err := event.NewRouter(logger)
	if err != nil {
		_ = bus.Close()

		return nil, err
	}

	router.AddConsumerHandler(
		"event-log-consumer",
		messaging.EventsTopic,
		bus.Subscriber(),
		logEvent(logger),
	)

	router.AddConsumerHandler(
		"email-notifier",
		messaging.EventsTopic,
		bus.Subscriber(),
		event.EmailNotifier(memberStore, mailer, baseURL, logger),
	)

	router.AddConsumerHandler(
		"delivery-notifier",
		messaging.EventsTopic,
		bus.Subscriber(),
		event.DeliveryNotifier(memberStore, candidateStore, reg, providerMessageStore, surfaceManager, mailer, attachmentStore, blobs, baseURL, logger),
	)

	router.AddConsumerHandler(
		"invite-notifier",
		messaging.EventsTopic,
		bus.Subscriber(),
		event.InviteNotifier(memberStore, projectStore, identity.NewOrganizationStore(pool), inviteLinks, mailer, baseURL, logger),
	)

	router.AddConsumerHandler(
		"closure-notifier",
		messaging.EventsTopic,
		bus.Subscriber(),
		event.ClosureNotifier(memberStore, mailer, baseURL, logger),
	)

	router.AddConsumerHandler(
		"bridge-projector",
		messaging.EventsTopic,
		bus.Subscriber(),
		event.BridgeProjector(providerMessageStore, reg, memberStore, surfaceManager, logger),
	)

	router.AddConsumerHandler(
		"fanout-projector",
		messaging.EventsTopic,
		bus.Subscriber(),
		event.FanoutProjector(convStore, reg, memberStore, surfaceManager, attachmentStore, blobs, logger),
	)

	projectAddedNotifier := event.NewProjectAddedNotifier(
		memberStore,
		projectStore,
		identity.NewOrganizationStore(pool),
		mailer,
		baseURL,
		0,
		logger,
	)
	router.AddConsumerHandler(
		"project-added-notifier",
		messaging.EventsTopic,
		bus.Subscriber(),
		projectAddedNotifier.Handler(),
	)

	obs := metrics.NewFromEnv(os.Getenv("ORAKO_METRICS_KIND"), logger)
	addMember := command.MustNewAddMemberHandler(memberStore, txor, memberStore, bus, seatGate, slackDirectory)

	return &App{
		Commands: Commands{
			Ask: command.MustNewAskHandler(
				openConvStore, convStore, bus, reg, candidateStore, memberStore, projectStore, txor, attachmentStore,
			),
			FollowUp:            command.MustNewFollowUpHandler(convStore, bus, candidateStore, convStore, txor, attachmentStore),
			UploadAttachment:    command.MustNewUploadAttachmentHandler(convStore, convStore, candidateStore, attachmentStore, blobs),
			AddParticipant:      command.MustNewAddParticipantHandler(convStore, convStore, projectStore, bus),
			ResolveConversation: decorator.Apply[command.ResolveConversationCommand, command.ResolveConversationResult]("ResolveConversation", command.MustNewResolveConversationHandler(convStore, txor, bus), logger, obs),
			DismissConversation: decorator.Apply[command.DismissConversationCommand, command.DismissConversationResult]("DismissConversation", command.MustNewDismissConversationHandler(convStore), logger, obs),
			Heartbeat:           command.MustNewHeartbeatHandler(presenceStore, bus),
			CreateOrganization:  command.MustNewCreateOrganizationHandler(orgStore, projectStore, memberStore, txor, identity.NewAccountStore(pool), saasOrgHook, orgGate),
			CreateProject:       command.MustNewCreateProjectHandler(projectStore, txor, projGate),
			RenameProject:       command.MustNewRenameProjectHandler(projectStore),
			SetProjectArchived:  command.MustNewSetProjectArchivedHandler(projectStore),
			DeleteProject:       command.MustNewDeleteProjectHandler(projectStore),
			DeleteOrganization:  command.MustNewDeleteOrganizationHandler(orgStore, txor),
			DeleteConversation:  command.MustNewDeleteConversationHandler(convStore, projectStore, surfaceManager),
			AddMember:           addMember,
			AssignRole:          command.MustNewAssignRoleHandler(projectStore, bus),
			SetOwnExpertise:     command.MustNewSetOwnExpertiseHandler(projectStore),
			RemoveMember:        command.MustNewRemoveMemberHandler(memberStore, bus),

			SetMemberAvailability: command.MustNewSetMemberAvailabilityHandler(memberStore),
			SetMemberActivation:   command.MustNewSetMemberActivationHandler(memberStore),
			SetOrgAdmin:           command.MustNewSetOrgAdminHandler(memberStore, orgStore),
			SyncChatBindings:      command.MustNewSyncChatBindingsHandler(memberStore, slackDirectory),
			BindMemberChannel:     command.MustNewBindMemberChannelHandler(memberStore),
			InviteMembers:         command.MustNewInviteMembersHandler(addMember),
			ConfigureProvider:     command.MustNewConfigureProviderHandler(integration.NewOrgProviderStore(pool, credCipher), providerStore, reg, bus),
			DisconnectProvider:    command.MustNewDisconnectProviderHandler(providerStore, reg),
			SendProviderTest:      command.MustNewSendProviderTestHandler(providerKindResolver{reg: reg}, providerStore),
			UpdateMember:          command.MustNewUpdateMemberHandler(memberStore, projectStore),
			RedeemJoinToken:       command.MustNewRedeemJoinTokenHandler(joinTokenStore, memberStore, memberStore, txor, seatGate),
			GenerateJoinCode:      command.MustNewGenerateJoinCodeHandler(joinTokenStore, projectStore),
			RevokeJoinCode:        command.MustNewRevokeJoinCodeHandler(joinTokenStore),

			UpdateOrganizationSettings: command.MustNewUpdateOrganizationSettingsHandler(escalationStore),

			RenameOrganization: command.MustNewRenameOrganizationHandler(orgStore),

			CreateKnowledgeEntry:           command.MustNewCreateKnowledgeEntryHandler(knowledgeStore),
			UpdateKnowledgeEntry:           command.MustNewUpdateKnowledgeEntryHandler(knowledgeStore),
			MarkKnowledgeStale:             command.MustNewMarkKnowledgeStaleHandler(knowledgeStore),
			SuggestKnowledgeEntry:          command.MustNewSuggestKnowledgeEntryHandler(knowledgeStore),
			ApproveKnowledgeEntry:          command.MustNewApproveKnowledgeEntryHandler(knowledgeStore),
			DismissKnowledgeEntry:          command.MustNewDismissKnowledgeEntryHandler(knowledgeStore),
			RevalidateKnowledgeEntry:       command.MustNewRevalidateKnowledgeEntryHandler(knowledgeStore),
			PromoteConversationToKnowledge: command.MustNewPromoteConversationToKnowledgeHandler(convStore, knowledgeStore),
		},
		Queries: Queries{
			SearchHistory:            query.MustNewSearchHistoryHandler(convStore).WithCurated(knowledgeStore),
			HistoryStatusCounts:      query.MustNewHistoryStatusCountsHandler(convStore),
			GetDashboardMetrics:      query.MustNewGetDashboardMetricsHandler(convStore),
			HistoryVocabulary:        query.MustNewHistoryVocabularyHandler(convStore),
			ListExperts:              query.MustNewListExpertsHandler(projectStore, memberStore, presenceStore),
			GetConversation:          decorator.Apply[query.GetConversationQuery, query.ConversationView]("GetConversation", query.MustNewGetConversationHandler(convStore, candidateStore, convStore, convStore, attachmentStore, blobs), logger, obs),
			ListProjects:             query.MustNewListProjectsHandler(projectStore),
			ListProjectsDetailed:     query.MustNewListProjectsDetailedHandler(projectStore, providerStore),
			ListConversations:        query.MustNewListConversationsHandler(convStore, convStore, convStore),
			ListInbox:                query.MustNewListInboxHandler(convStore, candidateStore),
			GetMember:                query.MustNewGetMemberHandler(memberStore),
			ListMembers:              query.MustNewListMembersHandler(memberStore),
			GetOrgMember:             query.MustNewGetOrgMemberHandler(memberStore),
			ListConnectedChannels:    query.MustNewListConnectedChannelsHandler(providerStore),
			GetProviderAlertChannels: query.MustNewGetProviderAlertChannelsHandler(providerStore),

			GetOrganizationSettings: query.MustNewGetOrganizationSettingsHandler(escalationStore),
			GetOrganization:         query.MustNewGetOrganizationHandler(orgStore),
			ListOrganizations:       query.MustNewListOrganizationsHandler(orgStore),
			GetJoinCode:             query.MustNewGetJoinCodeHandler(joinTokenStore, projectStore),
			ListKnowledgeEntries:    query.MustNewListKnowledgeEntriesHandler(knowledgeStore),
			ListPendingKnowledge:    query.MustNewListPendingKnowledgeEntriesHandler(knowledgeStore),
		},
		Events: bus,
		Escalation: event.NewEscalationSweeper(
			escalationStore, candidateStore, convStore, bus, memberStore, reg, mailer, baseURL, logger,
		),
		router:               router,
		relay:                relay,
		projectAddedNotifier: projectAddedNotifier,
		logger:               logger,
	}, nil
}

// RunEvents runs the event router and outbox relay until ctx is cancelled.
func (a *App) RunEvents(ctx context.Context) error {
	if err := a.relay.Seed(ctx); err != nil {
		return fmt.Errorf("seeding outbox relay: %w", err)
	}

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-a.router.Running():
		}

		a.relay.Run(ctx)
	}()

	if err := a.router.Run(ctx); err != nil {
		return fmt.Errorf("running event router: %w", err)
	}

	return nil
}

// Running closes once the event router is ready.
func (a *App) Running() <-chan struct{} {
	return a.router.Running()
}

// Close stops application-owned resources.
func (a *App) Close() error {
	a.projectAddedNotifier.Close()

	return errors.Join(a.router.Close(), a.Events.Close())
}

func logEvent(logger *slog.Logger) message.NoPublishHandlerFunc {
	return func(msg *message.Message) error {
		env, err := messaging.DecodeEnvelope(msg.Payload)
		if err != nil {
			return err
		}

		logger.DebugContext(
			msg.Context(), "event received",
			slog.String("event_id", env.GetEventId()),
			slog.String("project_id", env.GetProjectId()),
			slog.String("type", env.GetType().String()),
			slog.Int64("seq", env.GetSeq()),
		)

		return nil
	}
}
