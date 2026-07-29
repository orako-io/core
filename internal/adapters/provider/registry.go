// SPDX-License-Identifier: AGPL-3.0-or-later

// Package provider is the per-project messaging provider registry: it selects
// the concrete adapter (Slack, Teams, Telegram, or noop) behind the domain's
// service.Provider port.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/provider/dashboard"
	"github.com/orako-io/core/internal/adapters/provider/discord"
	"github.com/orako-io/core/internal/adapters/provider/slack"
	"github.com/orako-io/core/internal/adapters/provider/teams"
	"github.com/orako-io/core/internal/adapters/provider/telegram"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

// ProviderKind identifies which messaging provider is used for a project.
//
//nolint:revive // exported name stutters (provider.ProviderKind); renaming would break external callers
type ProviderKind string

const (
	// ProviderKindSlack selects the real Slack v1 adapter.
	ProviderKindSlack ProviderKind = "slack"
	// ProviderKindTeams selects the real Microsoft Teams adapter (Bot Framework).
	ProviderKindTeams ProviderKind = "teams"
	// ProviderKindTelegram selects the Telegram Bot API stub.
	ProviderKindTelegram ProviderKind = "telegram"
	// ProviderKindDiscord selects the real Discord adapter (REST + gateway).
	ProviderKindDiscord ProviderKind = "discord"
	// ProviderKindNoop selects the no-op provider for tests and local development.
	ProviderKindNoop ProviderKind = "noop"
)

// ProjectConfig binds a project UUID to its provider kind and credentials.
// Only the field matching Kind must be populated; the rest are optional/nil.
type ProjectConfig struct {
	ProjectID uuid.UUID `exhaustruct:"optional"` // optional for noop kind
	Kind      ProviderKind
	Slack     *slack.Config    `exhaustruct:"optional"` // nil unless Kind==ProviderKindSlack
	Teams     *teams.Config    `exhaustruct:"optional"` // nil unless Kind==ProviderKindTeams
	Telegram  *telegram.Config `exhaustruct:"optional"` // nil unless Kind==ProviderKindTelegram
	Discord   *discord.Config  `exhaustruct:"optional"` // nil unless Kind==ProviderKindDiscord
}

// MemberLookup is the member-side dependency used when constructing adapters.
type MemberLookup interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Member, error)
	BySlackUserID(ctx context.Context, slackUserID string) (model.Member, error)
	ByTelegramChatID(ctx context.Context, telegramChatID string) (model.Member, error)
	ByTeamsUserID(ctx context.Context, teamsUserID string) (model.Member, error)
	ByDiscordUserID(ctx context.Context, discordUserID string) (model.Member, error)
}

// ConversationAccess is the conversation-side dependency used when
// constructing adapters.
type ConversationAccess interface {
	ConversationBySlackThread(ctx context.Context, channelID, threadTS string) (model.Conversation, error)
	SetSlackThread(ctx context.Context, id uuid.UUID, channelID, threadTS string) error
	ConversationByTelegramMessage(ctx context.Context, chatID, messageID string) (model.Conversation, error)
	SetTelegramThread(ctx context.Context, id uuid.UUID, chatID, messageID string) error
}

// LedgerReader resolves a delivered provider message's conversation from its
// channel/ref — the pool-DM delivery ledger. Used for inbound correlation
// fallback when the direct conversations-thread lookup misses.
type LedgerReader interface {
	ByProviderRef(ctx context.Context, channelID, messageRef string) (model.ProviderMessage, error)
	// LatestByChannel resolves the most recently updated open ledger row for
	// a channel — used by adapters (Teams) whose conversation model has no
	// stable per-message reference to key an exact ByProviderRef match.
	LatestByChannel(ctx context.Context, channelID string) (model.ProviderMessage, error)
}

// Deps holds the driven-adapter dependencies provider constructors need.
// All fields may be nil for registries that only use the noop provider.
type Deps struct {
	Members       MemberLookup       `exhaustruct:"optional"`
	Conversations ConversationAccess `exhaustruct:"optional"`
	Ledger        LedgerReader       `exhaustruct:"optional"`

	// Webhooks caches the per-channel Discord webhook pair identity
	// mirroring uses (hub-and-spoke phase 5). nil disables identity posting
	// (mirrors then fall back to plain bot posts).
	Webhooks DiscordWebhookCache `exhaustruct:"optional"`

	// DashboardBaseURL is the Orako server's externally reachable base URL
	// (ORAKO_BASE_URL), passed to every provider so a delivered message can
	// link back to the full conversation in the dashboard. Empty when the base
	// URL is unconfigured (local dev); providers then fall back to a plain
	// "in the dashboard" pointer.
	DashboardBaseURL string `exhaustruct:"optional"`
}

// DiscordWebhookCache persists the one-webhook-per-channel pair the Discord
// adapter's identity mirroring uses. *conversation.SurfaceStore satisfies it.
type DiscordWebhookCache interface {
	WebhookForChannel(ctx context.Context, channelID string) (webhookID, token string, err error)
	SaveChannelWebhook(ctx context.Context, channelID, webhookID, token string) error
}

// ProviderLoader is the optional read-through dependency: when set,
// ForProjectKind loads a missing (project, kind) config from persistent
// storage on cache miss.
//
//nolint:revive // exported name stutters (provider.ProviderLoader); renaming would break external callers
type ProviderLoader interface {
	// LoadProvider returns the raw JSON credentials stored for the exact
	// (projectID, kind) pair. Returns adaptererr.ErrNotFound when that pair has
	// no stored row — even if the project has other kinds configured.
	LoadProvider(ctx context.Context, projectID uuid.UUID, kind string) (credentials []byte, err error)
}

// ErrNoProvider aliases service.ErrNoProvider for backward compatibility.
var ErrNoProvider = service.ErrNoProvider

// projectKey identifies one configured provider: a project can have several,
// one per kind (Slack + Discord + Teams simultaneously), so the registry's
// store must be keyed on the pair, never on ProjectID alone — a single-key
// map silently lets the last-registered kind evict every other kind
// previously configured for the same project.
type projectKey struct {
	ProjectID uuid.UUID
	Kind      ProviderKind
}

// Registry resolves the configured Provider for a project. Safe for concurrent
// use; an optional ProviderLoader enables read-through on cache miss.
type Registry struct {
	mu        sync.RWMutex `exhaustruct:"optional"`
	providers map[projectKey]service.Provider
	deps      Deps
	loader    ProviderLoader   `exhaustruct:"optional"` // nil disables read-through
	dashboard service.Provider // always-available in-app provider

	// registerHook, when set, is invoked at the end of every RegisterFromMap
	// call (kind and creds as given, unfiltered) — the seam the Discord
	// gateway supervisor uses to open/replace a session whenever a project's
	// discord credentials change. Never invoked from HydrateFrom or the
	// ForProjectKind read-through path; those are covered by the supervisor's
	// own boot-time sync against the provider store.
	registerHook func(projectID uuid.UUID, kind string, creds map[string]string) `exhaustruct:"optional"`
}

// SetRegisterHook installs the callback invoked at the end of every
// RegisterFromMap call. Not safe to call concurrently with RegisterFromMap;
// intended to be set once during startup wiring, before the registry is
// exposed to callers.
func (r *Registry) SetRegisterHook(hook func(projectID uuid.UUID, kind string, creds map[string]string)) {
	r.mu.Lock()
	r.registerHook = hook
	r.mu.Unlock()
}

// New builds a Registry from the supplied configs. loader is optional; nil
// disables read-through on cache miss.
func New(configs []ProjectConfig, deps Deps, loader ProviderLoader) (*Registry, error) {
	r := &Registry{
		providers: make(map[projectKey]service.Provider, len(configs)),
		deps:      deps,
		loader:    loader,
		dashboard: dashboard.New(),
	}

	for _, cfg := range configs {
		p, err := buildProvider(cfg, deps)
		if err != nil {
			return nil, fmt.Errorf("project %s: %w", cfg.ProjectID, err)
		}

		r.providers[projectKey{ProjectID: cfg.ProjectID, Kind: cfg.Kind}] = p
	}

	return r, nil
}

// HydrateFrom loads all configured providers from loader and registers them
// under their own (project, kind) key — a project with several kinds
// configured (e.g. Slack + Discord) hydrates all of them, none evicting
// another — so providers survive restarts. No-op if loader is nil.
func (r *Registry) HydrateFrom(ctx context.Context, loader service.AllProvidersLoader) error {
	if loader == nil {
		return nil
	}

	rows, err := loader.LoadAllProviders(ctx)
	if err != nil {
		return fmt.Errorf("registry: hydrating from store: %w", err)
	}

	for _, row := range rows {
		// A project-level provider row with no credentials is expected: the
		// connection's credentials live at the org level (org_providers) and are
		// resolved by read-through. Skip it rather than aborting hydration on an
		// empty-JSON decode error.
		if len(row.Credentials) == 0 {
			continue
		}

		var creds map[string]string

		if err := json.Unmarshal(row.Credentials, &creds); err != nil {
			return fmt.Errorf("registry: decoding credentials for project %s (%s): %w", row.ProjectID, row.Kind, err)
		}

		if err := r.RegisterFromMap(row.ProjectID, row.Kind, creds); err != nil {
			return fmt.Errorf("registry: registering project %s (%s): %w", row.ProjectID, row.Kind, err)
		}
	}

	return nil
}

// ForProjectKind returns the Provider configured for the exact (projectID,
// kind) pair — the one kind a caller who already knows which channel it is
// speaking on (an inbound webhook, the alert rung) must resolve. It never
// returns "some other kind configured for this project": a project with
// Slack, Discord, and Teams all configured resolves each kind independently,
// and a kind that was never configured is ErrNoProvider even when siblings
// exist. On cache miss it reads through the loader when one is set,
// otherwise returns ErrNoProvider.
func (r *Registry) ForProjectKind(ctx context.Context, projectID uuid.UUID, kind ProviderKind) (service.Provider, error) {
	key := projectKey{ProjectID: projectID, Kind: kind}

	r.mu.RLock()
	p, ok := r.providers[key]
	r.mu.RUnlock()

	if ok {
		return p, nil
	}

	if r.loader == nil {
		return nil, fmt.Errorf("%w: %s (%s)", ErrNoProvider, projectID, kind)
	}

	credBytes, err := r.loader.LoadProvider(ctx, projectID, string(kind))
	if err != nil {
		if errors.Is(err, adaptererr.ErrNotFound) {
			return nil, fmt.Errorf("%w: %s (%s)", ErrNoProvider, projectID, kind)
		}

		return nil, fmt.Errorf("provider: read-through load for %s (%s): %w", projectID, kind, err)
	}

	var creds map[string]string

	if err := json.Unmarshal(credBytes, &creds); err != nil {
		return nil, fmt.Errorf("provider: decoding credentials for %s (%s): %w", projectID, kind, err)
	}

	p, err = buildProviderFromMap(projectID, kind, creds, r.deps)
	if err != nil {
		return nil, fmt.Errorf("provider: building %s adapter for %s: %w", kind, projectID, err)
	}

	r.mu.Lock()
	r.providers[key] = p
	r.mu.Unlock()

	return p, nil
}

// deliveryChannelToProviderKind maps a member's delivery channel to the
// registry kind that serves it. ok is false for the dashboard channel (no
// external provider — the caller handles that case before calling this) and
// for any channel this registry does not know how to route externally.
func deliveryChannelToProviderKind(c model.DeliveryChannel) (ProviderKind, bool) {
	switch c {
	case model.DeliveryChannelSlack:
		return ProviderKindSlack, true
	case model.DeliveryChannelTeams:
		return ProviderKindTeams, true
	case model.DeliveryChannelTelegram:
		return ProviderKindTelegram, true
	case model.DeliveryChannelDiscord:
		return ProviderKindDiscord, true
	case model.DeliveryChannelDashboard:
		// The caller (ForMember) handles dashboard before reaching here; this
		// case only exists so the switch is exhaustive over DeliveryChannel.
		return "", false
	default:
		return "", false
	}
}

// ForMember resolves the Provider by the responder's delivery channel:
// dashboard members (and empty, a member persisted before the column
// existed) get the always-available in-app provider (never ErrNoProvider).
// An external channel resolves the provider configured for that exact kind
// on the project via ForProjectKind — never "whichever kind the project last
// registered" — so a Slack-bound member on a project that also has Discord
// configured still reaches Slack.
func (r *Registry) ForMember(ctx context.Context, projectID, memberID uuid.UUID) (service.Provider, error) {
	if r.deps.Members == nil {
		return nil, fmt.Errorf("%w: %s: no member lookup wired to resolve a delivery channel", ErrNoProvider, memberID)
	}

	member, err := r.deps.Members.ByID(ctx, memberID)
	if err != nil {
		return nil, fmt.Errorf("provider: resolving delivery channel for member %s: %w", memberID, err)
	}

	// Empty (a member persisted before the column existed) defaults to dashboard.
	if member.DeliveryChannel == model.DeliveryChannelDashboard || member.DeliveryChannel == "" {
		return r.dashboard, nil
	}

	kind, ok := deliveryChannelToProviderKind(member.DeliveryChannel)
	if !ok {
		return nil, fmt.Errorf("%w: member %s: unsupported delivery channel %q", ErrNoProvider, memberID, member.DeliveryChannel)
	}

	return r.ForProjectKind(ctx, projectID, kind)
}

// RegisterFromMap adds or replaces the provider for the (projectID, kind)
// pair from a credentials map. Safe for concurrent use. Registering one kind
// never evicts another kind already configured for the same project — e.g.
// registering Discord after Slack leaves the Slack entry resolvable.
func (r *Registry) RegisterFromMap(projectID uuid.UUID, kind string, creds map[string]string) error {
	p, err := buildProviderFromMap(projectID, ProviderKind(kind), creds, r.deps)
	if err != nil {
		return err
	}

	r.mu.Lock()
	r.providers[projectKey{ProjectID: projectID, Kind: ProviderKind(kind)}] = p
	hook := r.registerHook
	r.mu.Unlock()

	if hook != nil {
		hook(projectID, kind, creds)
	}

	return nil
}

// Unregister removes the in-memory provider for (projectID, kind) — used on
// DisconnectProvider so a removed provider stops delivering before the next
// restart. Safe for concurrent use; a no-op when nothing is registered.
func (r *Registry) Unregister(projectID uuid.UUID, kind string) {
	r.mu.Lock()
	delete(r.providers, projectKey{ProjectID: projectID, Kind: ProviderKind(kind)})
	r.mu.Unlock()
}

// Register adds or replaces the Slack provider for projectID (legacy OAuth
// callback path; prefer RegisterFromMap). Safe for concurrent use. Only
// touches the Slack entry for this project; any other kind configured for
// the same project is untouched.
func (r *Registry) Register(projectID uuid.UUID, signingSecret, botToken string) {
	p := slack.New(slack.Config{
		BotToken:         botToken,
		SigningSecret:    signingSecret,
		DashboardBaseURL: r.deps.DashboardBaseURL,
	}, r.deps.Members, r.deps.Conversations, r.deps.Ledger)

	r.mu.Lock()
	r.providers[projectKey{ProjectID: projectID, Kind: ProviderKindSlack}] = p
	r.mu.Unlock()
}

// buildProviderFromMap constructs a provider from a kind and a flat credentials map.
func buildProviderFromMap(projectID uuid.UUID, kind ProviderKind, creds map[string]string, deps Deps) (service.Provider, error) {
	switch kind {
	case ProviderKindSlack:
		return buildProvider(ProjectConfig{
			ProjectID: projectID,
			Kind:      kind,
			Slack: &slack.Config{
				BotToken:      creds["bot_token"],
				SigningSecret: creds["signing_secret"],
			},
		}, deps)

	case ProviderKindTeams:
		return buildProvider(ProjectConfig{
			ProjectID: projectID,
			Kind:      kind,
			Teams: &teams.Config{
				TenantID:     creds["tenant_id"],
				ClientID:     creds["client_id"],
				ClientSecret: creds["client_secret"],
				BotAppID:     creds["bot_app_id"],
				ServiceURL:   creds["service_url"],
			},
		}, deps)

	case ProviderKindTelegram:
		return buildProvider(ProjectConfig{
			ProjectID: projectID,
			Kind:      kind,
			Telegram: &telegram.Config{
				BotToken: creds["bot_token"],
			},
		}, deps)

	case ProviderKindDiscord:
		return buildProvider(ProjectConfig{
			ProjectID: projectID,
			Kind:      kind,
			Discord: &discord.Config{
				BotToken: creds["bot_token"],
			},
		}, deps)

	case ProviderKindNoop:
		return buildProvider(ProjectConfig{Kind: kind}, deps)

	default:
		return nil, fmt.Errorf("unknown provider kind: %q", kind)
	}
}

// buildProvider instantiates the correct provider from a ProjectConfig.
func buildProvider(cfg ProjectConfig, deps Deps) (service.Provider, error) {
	switch cfg.Kind {
	case ProviderKindSlack:
		if cfg.Slack == nil {
			return nil, fmt.Errorf("slack config required for kind %q", cfg.Kind)
		}

		cfg.Slack.DashboardBaseURL = deps.DashboardBaseURL

		return slack.New(*cfg.Slack, deps.Members, deps.Conversations, deps.Ledger), nil

	case ProviderKindTeams:
		if cfg.Teams == nil {
			return nil, fmt.Errorf("teams config required for kind %q", cfg.Kind)
		}

		cfg.Teams.DashboardBaseURL = deps.DashboardBaseURL

		return teams.New(*cfg.Teams, deps.Members, deps.Ledger), nil

	case ProviderKindTelegram:
		if cfg.Telegram == nil {
			return nil, fmt.Errorf("telegram config required for kind %q", cfg.Kind)
		}

		cfg.Telegram.DashboardBaseURL = deps.DashboardBaseURL

		return telegram.New(*cfg.Telegram, deps.Members, deps.Conversations), nil

	case ProviderKindDiscord:
		if cfg.Discord == nil {
			return nil, fmt.Errorf("discord config required for kind %q", cfg.Kind)
		}

		cfg.Discord.DashboardBaseURL = deps.DashboardBaseURL

		return discord.New(*cfg.Discord, deps.Members, deps.Webhooks), nil

	case ProviderKindNoop:
		return &noopProvider{}, nil

	default:
		return nil, fmt.Errorf("unknown provider kind: %q", cfg.Kind)
	}
}

// noopProvider is the in-memory provider for tests and local development.
// Deliver is a no-op; ParseInbound always returns an error.
type noopProvider struct{}

func (p *noopProvider) Deliver(_ context.Context, _ service.OutboundMessage) (service.MessageRef, error) {
	return service.MessageRef{ChannelID: "", MessageID: ""}, nil
}

func (p *noopProvider) ParseInbound(_ context.Context, _ []byte) (service.InboundMessage, error) {
	return service.InboundMessage{}, errors.New("noop provider: ParseInbound not wired")
}
