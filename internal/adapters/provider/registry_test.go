// SPDX-License-Identifier: AGPL-3.0-or-later

package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	provider "github.com/orako-io/core/internal/adapters/provider"
	"github.com/orako-io/core/internal/adapters/provider/discord"
	"github.com/orako-io/core/internal/adapters/provider/slack"
	"github.com/orako-io/core/internal/adapters/provider/teams"
	"github.com/orako-io/core/internal/adapters/provider/telegram"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

// ── test fakes ────────────────────────────────────────────────────────────────

type registryFakeMemberStore struct{}

func (f *registryFakeMemberStore) ByID(_ context.Context, _ uuid.UUID) (model.Member, error) {
	return model.Member{}, adaptererr.ErrNotFound
}

func (f *registryFakeMemberStore) BySlackUserID(_ context.Context, _ string) (model.Member, error) {
	return model.Member{}, adaptererr.ErrNotFound
}

func (f *registryFakeMemberStore) ByTelegramChatID(_ context.Context, _ string) (model.Member, error) {
	return model.Member{}, adaptererr.ErrNotFound
}

func (f *registryFakeMemberStore) ByTeamsUserID(_ context.Context, _ string) (model.Member, error) {
	return model.Member{}, adaptererr.ErrNotFound
}

func (f *registryFakeMemberStore) ByDiscordUserID(_ context.Context, _ string) (model.Member, error) {
	return model.Member{}, adaptererr.ErrNotFound
}

type registryFakeConvStore struct{}

func (f *registryFakeConvStore) ConversationBySlackThread(_ context.Context, _, _ string) (model.Conversation, error) {
	return model.Conversation{}, adaptererr.ErrNotFound
}

func (f *registryFakeConvStore) SetSlackThread(_ context.Context, _ uuid.UUID, _, _ string) error {
	return nil
}

func (f *registryFakeConvStore) ConversationByTelegramMessage(_ context.Context, _, _ string) (model.Conversation, error) {
	return model.Conversation{}, adaptererr.ErrNotFound
}

func (f *registryFakeConvStore) SetTelegramThread(_ context.Context, _ uuid.UUID, _, _ string) error {
	return nil
}

func testDeps() provider.Deps {
	return provider.Deps{
		Members:       &registryFakeMemberStore{},
		Conversations: &registryFakeConvStore{},
	}
}

// fakeProviderLoader implements provider.ProviderLoader for read-through
// tests. rows is keyed on the exact (projectID, kind) pair it now must
// resolve — a project can have several kinds stored, and a lookup for one
// kind must never be satisfied by another kind's row.
type fakeProviderLoader struct {
	rows    map[fakeProviderLoaderKey]map[string]string
	loadErr error
}

type fakeProviderLoaderKey struct {
	projectID uuid.UUID
	kind      string
}

func (f *fakeProviderLoader) LoadProvider(_ context.Context, projectID uuid.UUID, kind string) ([]byte, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}

	creds, ok := f.rows[fakeProviderLoaderKey{projectID: projectID, kind: kind}]
	if !ok {
		return nil, fmt.Errorf("project %s kind %s: %w", projectID, kind, adaptererr.ErrNotFound)
	}

	b, _ := json.Marshal(creds)

	return b, nil
}

// fakeAllProvidersLoader implements service.AllProvidersLoader for hydration tests.
type fakeAllProvidersLoader struct {
	rows    []service.ProviderRow
	loadErr error
}

func (f *fakeAllProvidersLoader) LoadAllProviders(_ context.Context) ([]service.ProviderRow, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}

	return f.rows, nil
}

// providerRow builds a service.ProviderRow from a kind + credential map.
func providerRow(projectID uuid.UUID, kind string, creds map[string]string) service.ProviderRow {
	b, _ := json.Marshal(creds)

	return service.ProviderRow{ProjectID: projectID, Kind: kind, Credentials: b}
}

// ── registry resolution (existing) tests ─────────────────────────────────────

// TestRegistry_ForProjectKind verifies that ForProjectKind returns the
// correct provider for configured (project, kind) pairs and ErrNoProvider for
// an unknown project or a kind never configured for it.
func TestRegistry_ForProjectKind(t *testing.T) {
	t.Parallel()

	slackProjectID := uuid.New()
	teamsProjectID := uuid.New()
	telegramProjectID := uuid.New()
	discordProjectID := uuid.New()
	noopProjectID := uuid.New()
	unknownProjectID := uuid.New()

	configs := []provider.ProjectConfig{
		{
			ProjectID: slackProjectID,
			Kind:      provider.ProviderKindSlack,
			Slack:     &slack.Config{BotToken: "xoxb-test"},
		},
		{
			ProjectID: teamsProjectID,
			Kind:      provider.ProviderKindTeams,
			Teams:     &teams.Config{TenantID: "t", ClientID: "c", ClientSecret: "s", BotAppID: "b"},
		},
		{
			ProjectID: telegramProjectID,
			Kind:      provider.ProviderKindTelegram,
			Telegram:  &telegram.Config{BotToken: "12345:ABC"},
		},
		{
			ProjectID: discordProjectID,
			Kind:      provider.ProviderKindDiscord,
			Discord:   &discord.Config{BotToken: "discord-test-token"},
		},
		{
			ProjectID: noopProjectID,
			Kind:      provider.ProviderKindNoop,
		},
	}

	reg, err := provider.New(configs, testDeps(), nil) // nil = no read-through
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := []struct {
		name       string
		projectID  uuid.UUID
		kind       provider.ProviderKind
		wantErr    bool
		wantNoProv bool
	}{
		{name: "slack_project", projectID: slackProjectID, kind: provider.ProviderKindSlack},
		{name: "teams_project", projectID: teamsProjectID, kind: provider.ProviderKindTeams},
		{name: "telegram_project", projectID: telegramProjectID, kind: provider.ProviderKindTelegram},
		{name: "discord_project", projectID: discordProjectID, kind: provider.ProviderKindDiscord},
		{name: "noop_project", projectID: noopProjectID, kind: provider.ProviderKindNoop},
		{name: "unknown_project", projectID: unknownProjectID, kind: provider.ProviderKindSlack, wantErr: true, wantNoProv: true},
		{name: "known_project_wrong_kind", projectID: slackProjectID, kind: provider.ProviderKindDiscord, wantErr: true, wantNoProv: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := reg.ForProjectKind(context.Background(), tc.projectID, tc.kind)

			if tc.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}

				if tc.wantNoProv && !errors.Is(err, provider.ErrNoProvider) {
					t.Errorf("error: got %v, want errors.Is(err, ErrNoProvider)", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("ForProjectKind: %v", err)
			}

			if p == nil {
				t.Fatal("ForProjectKind returned nil provider")
			}
		})
	}
}

// TestRegistry_SlackProviderIsUsable verifies that the slack provider returned
// by the registry actually implements the full Provider contract (not just
// non-nil). We verify by calling Deliver and observing the expected error
// (ErrNotFound for the missing member, not a nil-pointer or wrong-type panic).
func TestRegistry_SlackProviderIsUsable(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	reg, err := provider.New([]provider.ProjectConfig{{
		ProjectID: projectID,
		Kind:      provider.ProviderKindSlack,
		Slack:     &slack.Config{BotToken: "xoxb-test"},
	}}, testDeps(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	p, err := reg.ForProjectKind(context.Background(), projectID, provider.ProviderKindSlack)
	if err != nil {
		t.Fatalf("ForProjectKind: %v", err)
	}

	// The fake member store always returns ErrNotFound; Deliver should propagate
	// that rather than panicking.
	_, deliverErr := p.Deliver(context.Background(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: uuid.New(),
		Question:          "test",
	})

	if deliverErr == nil {
		t.Fatal("want error from Deliver (member not found), got nil")
	}

	// The error must NOT be ErrNoProvider (that would indicate wrong routing).
	if errors.Is(deliverErr, provider.ErrNoProvider) {
		t.Errorf("Deliver error should not be ErrNoProvider: %v", deliverErr)
	}
}

// TestRegistry_New_MissingConfig verifies that New returns an error when a
// required config struct is absent for the requested kind.
func TestRegistry_New_MissingConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		config provider.ProjectConfig
	}{
		{
			name:   "slack_nil_config",
			config: provider.ProjectConfig{ProjectID: uuid.New(), Kind: provider.ProviderKindSlack, Slack: nil},
		},
		{
			name:   "teams_nil_config",
			config: provider.ProjectConfig{ProjectID: uuid.New(), Kind: provider.ProviderKindTeams, Teams: nil},
		},
		{
			name:   "telegram_nil_config",
			config: provider.ProjectConfig{ProjectID: uuid.New(), Kind: provider.ProviderKindTelegram, Telegram: nil},
		},
		{
			name:   "discord_nil_config",
			config: provider.ProjectConfig{ProjectID: uuid.New(), Kind: provider.ProviderKindDiscord, Discord: nil},
		},
		{
			name:   "unknown_kind",
			config: provider.ProjectConfig{ProjectID: uuid.New(), Kind: "xmpp"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := provider.New([]provider.ProjectConfig{tc.config}, testDeps(), nil)
			if err == nil {
				t.Fatal("want error for missing config, got nil")
			}
		})
	}
}

// TestRegistry_TeamsProviderIsUsable verifies that the real Teams provider
// returned by the registry implements the full Provider contract. Deliver
// should propagate the fake member store's ErrNotFound (responder not
// found) rather than panic or return ErrNoProvider — the registry wired the
// real adapter, not a stub.
func TestRegistry_TeamsProviderIsUsable(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	reg, err := provider.New([]provider.ProjectConfig{{
		ProjectID: projectID,
		Kind:      provider.ProviderKindTeams,
		Teams:     &teams.Config{TenantID: "t", ClientID: "c", ClientSecret: "s", BotAppID: "b"},
	}}, testDeps(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	p, err := reg.ForProjectKind(context.Background(), projectID, provider.ProviderKindTeams)
	if err != nil {
		t.Fatalf("ForProjectKind: %v", err)
	}

	_, deliverErr := p.Deliver(context.Background(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: uuid.New(),
		Question:          "test",
	})
	if deliverErr == nil {
		t.Fatal("want error from Deliver (member not found), got nil")
	}

	if errors.Is(deliverErr, provider.ErrNoProvider) {
		t.Errorf("Deliver error should not be ErrNoProvider: %v", deliverErr)
	}
}

// TestRegistry_DiscordProviderIsUsable mirrors the Teams/Slack/Telegram
// contract check for the Discord adapter.
func TestRegistry_DiscordProviderIsUsable(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	reg, err := provider.New([]provider.ProjectConfig{{
		ProjectID: projectID,
		Kind:      provider.ProviderKindDiscord,
		Discord:   &discord.Config{BotToken: "discord-test-token"},
	}}, testDeps(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	p, err := reg.ForProjectKind(context.Background(), projectID, provider.ProviderKindDiscord)
	if err != nil {
		t.Fatalf("ForProjectKind: %v", err)
	}

	_, deliverErr := p.Deliver(context.Background(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: uuid.New(),
		Question:          "test",
	})
	if deliverErr == nil {
		t.Fatal("want error from Deliver (member not found), got nil")
	}

	if errors.Is(deliverErr, provider.ErrNoProvider) {
		t.Errorf("Deliver error should not be ErrNoProvider: %v", deliverErr)
	}

	// Discord has no inbound webhook (see discord.Provider.ParseInbound doc):
	// replies and button clicks arrive exclusively over the gateway.
	if _, perr := p.ParseInbound(context.Background(), []byte("{}")); !errors.Is(perr, service.ErrUnrecognizedMessage) {
		t.Errorf("Discord ParseInbound: got %v, want ErrUnrecognizedMessage", perr)
	}
}

// TestRegistry_TelegramProviderIsUsable verifies that the real Telegram provider
// returned by the registry implements the full Provider contract. Deliver should
// propagate ErrNotFound (responder not found) rather than panic or return ErrNoProvider.
func TestRegistry_TelegramProviderIsUsable(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	reg, err := provider.New([]provider.ProjectConfig{{
		ProjectID: projectID,
		Kind:      provider.ProviderKindTelegram,
		Telegram:  &telegram.Config{BotToken: "token"},
	}}, testDeps(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	p, err := reg.ForProjectKind(context.Background(), projectID, provider.ProviderKindTelegram)
	if err != nil {
		t.Fatalf("ForProjectKind: %v", err)
	}

	// The fake member store returns ErrNotFound; Deliver must propagate that,
	// not panic or return ErrNoProvider.
	_, deliverErr := p.Deliver(context.Background(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: uuid.New(),
		Question:          "test",
	})
	if deliverErr == nil {
		t.Fatal("want error from Deliver (member not found), got nil")
	}

	if errors.Is(deliverErr, provider.ErrNoProvider) {
		t.Errorf("Deliver error should not be ErrNoProvider: %v", deliverErr)
	}
}

// ── HydrateFrom tests ─────────────────────────────────────────────────────────

func TestRegistry_HydrateFrom_PopulatesProviders(t *testing.T) {
	t.Parallel()

	slackID := uuid.New()
	telegramID := uuid.New()

	loader := &fakeAllProvidersLoader{
		rows: []service.ProviderRow{
			providerRow(slackID, "slack", map[string]string{
				"bot_token":      "xoxb-hydrated",
				"signing_secret": "s3cr3t",
			}),
			providerRow(telegramID, "telegram", map[string]string{
				"bot_token": "123:ABC",
			}),
		},
	}

	reg, err := provider.New(nil, testDeps(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := reg.HydrateFrom(context.Background(), loader); err != nil {
		t.Fatalf("HydrateFrom: %v", err)
	}

	p, err := reg.ForProjectKind(context.Background(), slackID, provider.ProviderKindSlack)
	if err != nil {
		t.Fatalf("ForProjectKind(slack): %v", err)
	}

	if p == nil {
		t.Fatal("ForProjectKind(slack) returned nil")
	}

	p2, err := reg.ForProjectKind(context.Background(), telegramID, provider.ProviderKindTelegram)
	if err != nil {
		t.Fatalf("ForProjectKind(telegram): %v", err)
	}

	if p2 == nil {
		t.Fatal("ForProjectKind(telegram) returned nil")
	}
}

// TestRegistry_HydrateFrom_SkipsEmptyCredentials proves a project-level row with
// NULL/empty credentials (its connection lives at the org level) is skipped, not
// treated as a decode error that aborts hydration for every later row.
func TestRegistry_HydrateFrom_SkipsEmptyCredentials(t *testing.T) {
	t.Parallel()

	emptyID := uuid.New()
	slackID := uuid.New()

	loader := &fakeAllProvidersLoader{
		rows: []service.ProviderRow{
			{ProjectID: emptyID, Kind: "discord", Credentials: nil}, // NULL creds — must be skipped
			providerRow(slackID, "slack", map[string]string{
				"bot_token":      "xoxb-hydrated",
				"signing_secret": "s3cr3t",
			}),
		},
	}

	reg, err := provider.New(nil, testDeps(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Must NOT error on the empty row (previously aborted the whole loop).
	if err := reg.HydrateFrom(context.Background(), loader); err != nil {
		t.Fatalf("HydrateFrom must skip empty creds, got: %v", err)
	}

	// The row AFTER the empty one must still be hydrated.
	p, err := reg.ForProjectKind(context.Background(), slackID, provider.ProviderKindSlack)
	if err != nil || p == nil {
		t.Fatalf("row after the empty one was not hydrated: p=%v err=%v", p, err)
	}
}

// TestRegistry_HydrateFrom_MultiKindSameProject proves the review fit#4 fix at
// the hydration boundary: a project stored with two provider kinds (Slack and
// Discord) hydrates both, and each resolves independently — hydration order
// must never let the second kind evict the first from the in-memory registry.
func TestRegistry_HydrateFrom_MultiKindSameProject(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()

	loader := &fakeAllProvidersLoader{
		rows: []service.ProviderRow{
			providerRow(projectID, "slack", map[string]string{
				"bot_token":      "xoxb-multi",
				"signing_secret": "s3cr3t",
			}),
			providerRow(projectID, "discord", map[string]string{
				"bot_token": "discord-multi-token",
			}),
		},
	}

	reg, err := provider.New(nil, testDeps(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := reg.HydrateFrom(context.Background(), loader); err != nil {
		t.Fatalf("HydrateFrom: %v", err)
	}

	slackProv, err := reg.ForProjectKind(context.Background(), projectID, provider.ProviderKindSlack)
	if err != nil {
		t.Fatalf("ForProjectKind(slack) after multi-kind hydrate: %v", err)
	}

	if slackProv == nil {
		t.Fatal("ForProjectKind(slack) returned nil after multi-kind hydrate")
	}

	discordProv, err := reg.ForProjectKind(context.Background(), projectID, provider.ProviderKindDiscord)
	if err != nil {
		t.Fatalf("ForProjectKind(discord) after multi-kind hydrate: %v", err)
	}

	if discordProv == nil {
		t.Fatal("ForProjectKind(discord) returned nil after multi-kind hydrate")
	}
}

func TestRegistry_HydrateFrom_NilLoader(t *testing.T) {
	t.Parallel()

	reg, err := provider.New(nil, testDeps(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// HydrateFrom(nil) must be a safe no-op.
	if err := reg.HydrateFrom(context.Background(), nil); err != nil {
		t.Fatalf("HydrateFrom(nil): %v", err)
	}
}

func TestRegistry_HydrateFrom_StoreError(t *testing.T) {
	t.Parallel()

	loader := &fakeAllProvidersLoader{loadErr: errors.New("db: connection refused")}

	reg, err := provider.New(nil, testDeps(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := reg.HydrateFrom(context.Background(), loader); err == nil {
		t.Fatal("expected error from HydrateFrom when loader fails, got nil")
	}
}

// TestRegistry_HydrateFrom_OverwritesSameKindEntry proves HydrateFrom still
// replaces an existing entry for the SAME (project, kind) key — the registry
// is keyed on the pair, not the project alone, so this only holds within one
// kind; a different kind for the same project must NOT be evicted (see
// TestRegistry_HydrateFrom_MultiKindSameProject and
// TestRegistry_RegisterFromMap_SecondKindDoesNotEvictFirst).
func TestRegistry_HydrateFrom_OverwritesSameKindEntry(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()

	// Start with a Slack provider for the project (in-memory, pre-hydrate).
	reg, err := provider.New([]provider.ProjectConfig{{
		ProjectID: projectID,
		Kind:      provider.ProviderKindSlack,
		Slack:     &slack.Config{BotToken: "xoxb-original"},
	}}, testDeps(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Hydrate a Slack row for the same (project, kind) — must replace, not duplicate or error.
	loader := &fakeAllProvidersLoader{
		rows: []service.ProviderRow{
			providerRow(projectID, "slack", map[string]string{
				"bot_token":      "xoxb-overwrite",
				"signing_secret": "sig",
			}),
		},
	}

	if err := reg.HydrateFrom(context.Background(), loader); err != nil {
		t.Fatalf("HydrateFrom: %v", err)
	}

	// After hydration, ForProjectKind(slack) must return the hydrated provider
	// (no panic or error).
	p, err := reg.ForProjectKind(context.Background(), projectID, provider.ProviderKindSlack)
	if err != nil {
		t.Fatalf("ForProjectKind(slack) after hydrate: %v", err)
	}

	if p == nil {
		t.Fatal("ForProjectKind(slack) returned nil after hydration")
	}
}

// ── read-through tests ────────────────────────────────────────────────────────

func TestRegistry_ReadThrough_HitsLoaderOnCacheMiss(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()

	loader := &fakeProviderLoader{
		rows: map[fakeProviderLoaderKey]map[string]string{
			{projectID: projectID, kind: "slack"}: {
				"bot_token":      "xoxb-read-through",
				"signing_secret": "secret",
			},
		},
	}

	// Start with an empty registry (no static configs) but with a read-through loader.
	reg, err := provider.New(nil, testDeps(), loader)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// ForProjectKind triggers a read-through.
	p, err := reg.ForProjectKind(context.Background(), projectID, provider.ProviderKindSlack)
	if err != nil {
		t.Fatalf("ForProjectKind (read-through): %v", err)
	}

	if p == nil {
		t.Fatal("ForProjectKind returned nil after read-through")
	}

	// A second call must use the in-memory cache (loader is not called again).
	loader.rows = nil // wipe the loader — if cache is bypassed this panics/fails

	p2, err := reg.ForProjectKind(context.Background(), projectID, provider.ProviderKindSlack)
	if err != nil {
		t.Fatalf("ForProjectKind (from cache): %v", err)
	}

	if p2 == nil {
		t.Fatal("ForProjectKind returned nil on second call (expected from cache)")
	}
}

// TestRegistry_ReadThrough_KindExactMiss proves a read-through loader that has
// a project's Slack row does not satisfy a Discord lookup for the same
// project — the review fit#4 contract: a cache miss reads through by the
// exact (project, kind) pair, never "whatever this project has stored".
func TestRegistry_ReadThrough_KindExactMiss(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()

	loader := &fakeProviderLoader{
		rows: map[fakeProviderLoaderKey]map[string]string{
			{projectID: projectID, kind: "slack"}: {
				"bot_token":      "xoxb-kind-exact",
				"signing_secret": "secret",
			},
		},
	}

	reg, err := provider.New(nil, testDeps(), loader)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = reg.ForProjectKind(context.Background(), projectID, provider.ProviderKindDiscord)
	if !errors.Is(err, provider.ErrNoProvider) {
		t.Errorf("ForProjectKind(discord) on a slack-only project: got %v, want ErrNoProvider", err)
	}
}

func TestRegistry_ReadThrough_ReturnsErrNoProviderWhenLoaderMisses(t *testing.T) {
	t.Parallel()

	loader := &fakeProviderLoader{
		rows: map[fakeProviderLoaderKey]map[string]string{}, // nothing in store
	}

	reg, err := provider.New(nil, testDeps(), loader)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = reg.ForProjectKind(context.Background(), uuid.New(), provider.ProviderKindSlack)
	if !errors.Is(err, provider.ErrNoProvider) {
		t.Errorf("expected ErrNoProvider for unknown project, got: %v", err)
	}
}

func TestRegistry_ReadThrough_PropagatesLoaderError(t *testing.T) {
	t.Parallel()

	loaderErr := errors.New("db: query failed")
	loader := &fakeProviderLoader{loadErr: loaderErr}

	reg, err := provider.New(nil, testDeps(), loader)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = reg.ForProjectKind(context.Background(), uuid.New(), provider.ProviderKindSlack)
	if err == nil {
		t.Fatal("expected error from ForProjectKind when loader fails, got nil")
	}

	// The error should not be ErrNoProvider — the loader failed, not a miss.
	if errors.Is(err, provider.ErrNoProvider) {
		t.Errorf("got ErrNoProvider but expected a wrapped loader error: %v", err)
	}
}

func TestRegistry_NoLoader_ReturnsErrNoProvider(t *testing.T) {
	t.Parallel()

	// Without a loader, unknown projects immediately get ErrNoProvider.
	reg, err := provider.New(nil, testDeps(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = reg.ForProjectKind(context.Background(), uuid.New(), provider.ProviderKindSlack)
	if !errors.Is(err, provider.ErrNoProvider) {
		t.Errorf("expected ErrNoProvider without loader, got: %v", err)
	}
}

// ── RegisterFromMap tests ─────────────────────────────────────────────────────

func TestRegistry_RegisterFromMap_AddsSlackProvider(t *testing.T) {
	t.Parallel()

	reg, err := provider.New(nil, testDeps(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	projectID := uuid.New()

	if err := reg.RegisterFromMap(projectID, "slack", map[string]string{
		"bot_token":      "xoxb-runtime",
		"signing_secret": "sig",
	}); err != nil {
		t.Fatalf("RegisterFromMap: %v", err)
	}

	p, err := reg.ForProjectKind(context.Background(), projectID, provider.ProviderKindSlack)
	if err != nil {
		t.Fatalf("ForProjectKind after RegisterFromMap: %v", err)
	}

	if p == nil {
		t.Fatal("ForProjectKind returned nil after RegisterFromMap")
	}
}

func TestRegistry_RegisterFromMap_UnknownKindReturnsError(t *testing.T) {
	t.Parallel()

	reg, err := provider.New(nil, testDeps(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = reg.RegisterFromMap(uuid.New(), "xmpp", map[string]string{"host": "jabber.org"})
	if err == nil {
		t.Fatal("expected error for unknown kind, got nil")
	}
}

// TestRegistry_RegisterFromMap_SecondKindDoesNotEvictFirst proves the review
// fit#4 fix at the runtime-registration boundary: registering Discord for a
// project that already has Slack registered must not evict the Slack entry —
// the exact bug that sent a Slack-bound member's message to a failed Discord
// Deliver on a project whose last-registered kind was Discord.
func TestRegistry_RegisterFromMap_SecondKindDoesNotEvictFirst(t *testing.T) {
	t.Parallel()

	reg, err := provider.New(nil, testDeps(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	projectID := uuid.New()

	if err := reg.RegisterFromMap(projectID, "slack", map[string]string{
		"bot_token":      "xoxb-first",
		"signing_secret": "sig",
	}); err != nil {
		t.Fatalf("RegisterFromMap(slack): %v", err)
	}

	if err := reg.RegisterFromMap(projectID, "discord", map[string]string{
		"bot_token": "discord-second",
	}); err != nil {
		t.Fatalf("RegisterFromMap(discord): %v", err)
	}

	slackProv, err := reg.ForProjectKind(context.Background(), projectID, provider.ProviderKindSlack)
	if err != nil {
		t.Fatalf("ForProjectKind(slack) after registering discord: %v", err)
	}

	if slackProv == nil {
		t.Fatal("ForProjectKind(slack) returned nil after registering discord — slack was evicted")
	}

	discordProv, err := reg.ForProjectKind(context.Background(), projectID, provider.ProviderKindDiscord)
	if err != nil {
		t.Fatalf("ForProjectKind(discord): %v", err)
	}

	if discordProv == nil {
		t.Fatal("ForProjectKind(discord) returned nil")
	}
}

// ── per-member routing (ForMember) tests ─────────────────────────────────────

// routableMemberStore is a MemberLookup returning a configurable member by ID,
// used to drive ForMember's delivery-channel routing.
type routableMemberStore struct {
	member  model.Member
	byIDErr error
}

func (f *routableMemberStore) ByID(_ context.Context, _ uuid.UUID) (model.Member, error) {
	if f.byIDErr != nil {
		return model.Member{}, f.byIDErr
	}

	return f.member, nil
}

func (f *routableMemberStore) BySlackUserID(_ context.Context, _ string) (model.Member, error) {
	return model.Member{}, adaptererr.ErrNotFound
}

func (f *routableMemberStore) ByTelegramChatID(_ context.Context, _ string) (model.Member, error) {
	return model.Member{}, adaptererr.ErrNotFound
}

func (f *routableMemberStore) ByTeamsUserID(_ context.Context, _ string) (model.Member, error) {
	return model.Member{}, adaptererr.ErrNotFound
}

func (f *routableMemberStore) ByDiscordUserID(_ context.Context, _ string) (model.Member, error) {
	return model.Member{}, adaptererr.ErrNotFound
}

// TestRegistry_ForMember_DashboardAlwaysResolves is the orphan-bug guarantee:
// a responder on the dashboard channel resolves a provider even when the
// project has no external provider configured, so Ask/FollowUp never
// fail with ErrNoProvider for that member.
func TestRegistry_ForMember_DashboardAlwaysResolves(t *testing.T) {
	t.Parallel()

	members := &routableMemberStore{
		member: model.Member{DeliveryChannel: model.DeliveryChannelDashboard},
	}

	// No configs, no loader: an external channel would get ErrNoProvider.
	reg, err := provider.New(nil, provider.Deps{Members: members}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	prov, err := reg.ForMember(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("ForMember(dashboard) returned error: %v", err)
	}

	if prov == nil {
		t.Fatal("ForMember(dashboard) returned nil provider")
	}

	// The dashboard provider's Deliver is a successful no-op.
	if _, derr := prov.Deliver(context.Background(), service.OutboundMessage{
		ProjectID: uuid.New(), ConversationID: uuid.New(), ResponderMemberID: uuid.New(), Question: "q",
	}); derr != nil {
		t.Fatalf("dashboard Deliver returned error: %v", derr)
	}

	// The dashboard provider parses no inbound webhook (replies come via FollowUp).
	if _, perr := prov.ParseInbound(context.Background(), []byte("{}")); !errors.Is(perr, service.ErrUnrecognizedMessage) {
		t.Fatalf("dashboard ParseInbound = %v, want ErrUnrecognizedMessage", perr)
	}
}

// TestRegistry_ForMember_ExternalChannelDelegatesToForProjectKind verifies a
// member on an external channel still requires the project's matching-kind
// provider to be configured (ForProjectKind under the hood).
func TestRegistry_ForMember_ExternalChannelDelegatesToForProjectKind(t *testing.T) {
	t.Parallel()

	members := &routableMemberStore{
		member: model.Member{DeliveryChannel: model.DeliveryChannelSlack},
	}

	reg, err := provider.New(nil, provider.Deps{Members: members}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = reg.ForMember(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, service.ErrNoProvider) {
		t.Fatalf("ForMember(slack, unconfigured project) = %v, want ErrNoProvider", err)
	}
}

// TestRegistry_ForMember_EmptyChannelDefaultsToDashboard verifies a member row
// predating the delivery_channel column (empty value) is treated as dashboard.
func TestRegistry_ForMember_EmptyChannelDefaultsToDashboard(t *testing.T) {
	t.Parallel()

	members := &routableMemberStore{member: model.Member{}} // zero-value channel

	reg, err := provider.New(nil, provider.Deps{Members: members}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := reg.ForMember(context.Background(), uuid.New(), uuid.New()); err != nil {
		t.Fatalf("ForMember(empty channel) returned error: %v", err)
	}
}

// byIDMemberStore is a MemberLookup keyed by member id, used to drive
// scenarios with several distinct members (routableMemberStore always returns
// the same member regardless of id).
type byIDMemberStore struct {
	members map[uuid.UUID]model.Member
}

func (f *byIDMemberStore) ByID(_ context.Context, id uuid.UUID) (model.Member, error) {
	m, ok := f.members[id]
	if !ok {
		return model.Member{}, adaptererr.ErrNotFound
	}

	return m, nil
}

func (f *byIDMemberStore) BySlackUserID(_ context.Context, _ string) (model.Member, error) {
	return model.Member{}, adaptererr.ErrNotFound
}

func (f *byIDMemberStore) ByTelegramChatID(_ context.Context, _ string) (model.Member, error) {
	return model.Member{}, adaptererr.ErrNotFound
}

func (f *byIDMemberStore) ByTeamsUserID(_ context.Context, _ string) (model.Member, error) {
	return model.Member{}, adaptererr.ErrNotFound
}

func (f *byIDMemberStore) ByDiscordUserID(_ context.Context, _ string) (model.Member, error) {
	return model.Member{}, adaptererr.ErrNotFound
}

// TestRegistry_ForMember_MultiProviderProjectRoutesByChannel is the exact
// review fit#4 scenario: a project has Slack, Discord, and dashboard all
// resolvable at once. A Slack-bound member must reach the Slack provider, a
// Discord-bound member must reach the Discord provider, and a dashboard
// member must reach the dashboard provider — none of them must be routed to
// "whichever provider this project registered last".
func TestRegistry_ForMember_MultiProviderProjectRoutesByChannel(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	slackMemberID := uuid.New()
	discordMemberID := uuid.New()
	dashboardMemberID := uuid.New()

	members := &byIDMemberStore{members: map[uuid.UUID]model.Member{
		slackMemberID:     {ID: slackMemberID, DeliveryChannel: model.DeliveryChannelSlack, SlackUserID: "USLACK"},
		discordMemberID:   {ID: discordMemberID, DeliveryChannel: model.DeliveryChannelDiscord, DiscordUserID: "DDISCORD"},
		dashboardMemberID: {ID: dashboardMemberID, DeliveryChannel: model.DeliveryChannelDashboard},
	}}

	reg, err := provider.New([]provider.ProjectConfig{
		{
			ProjectID: projectID,
			Kind:      provider.ProviderKindSlack,
			Slack:     &slack.Config{BotToken: "xoxb-multi-routing"},
		},
		{
			ProjectID: projectID,
			Kind:      provider.ProviderKindDiscord,
			Discord:   &discord.Config{BotToken: "discord-multi-routing"},
		},
	}, provider.Deps{Members: members}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	slackWant, err := reg.ForProjectKind(context.Background(), projectID, provider.ProviderKindSlack)
	if err != nil {
		t.Fatalf("ForProjectKind(slack): %v", err)
	}

	discordWant, err := reg.ForProjectKind(context.Background(), projectID, provider.ProviderKindDiscord)
	if err != nil {
		t.Fatalf("ForProjectKind(discord): %v", err)
	}

	slackGot, err := reg.ForMember(context.Background(), projectID, slackMemberID)
	if err != nil {
		t.Fatalf("ForMember(slack-bound member): %v", err)
	}

	if slackGot != slackWant {
		t.Error("ForMember(slack-bound member) did not resolve the project's Slack provider")
	}

	discordGot, err := reg.ForMember(context.Background(), projectID, discordMemberID)
	if err != nil {
		t.Fatalf("ForMember(discord-bound member): %v", err)
	}

	if discordGot != discordWant {
		t.Error("ForMember(discord-bound member) did not resolve the project's Discord provider — routed to the wrong kind")
	}

	dashboardGot, err := reg.ForMember(context.Background(), projectID, dashboardMemberID)
	if err != nil {
		t.Fatalf("ForMember(dashboard-bound member): %v", err)
	}

	if dashboardGot == slackWant || dashboardGot == discordWant {
		t.Error("ForMember(dashboard-bound member) resolved an external provider instead of the dashboard provider")
	}
}
