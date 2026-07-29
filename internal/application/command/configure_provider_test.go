// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/pkg/errs"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// newHandlerUnderTest assembles a ConfigureProviderHandler backed by the given
// fakes. Passing nil for a fake uses a no-error stub.
func newHandlerUnderTest(
	orgStore *fakeOrgCredStore,
	channels *fakeProviderConfigStore,
	registry *fakeProviderRefresher,
	bus *fakeEventBus,
) ConfigureProviderHandler {
	if orgStore == nil {
		orgStore = &fakeOrgCredStore{}
	}

	if channels == nil {
		channels = &fakeProviderConfigStore{}
	}

	if registry == nil {
		registry = &fakeProviderRefresher{}
	}

	if bus == nil {
		bus = &fakeEventBus{}
	}

	return ConfigureProviderHandler{
		orgStore: orgStore,
		channels: channels,
		registry: registry,
		bus:      bus,
	}
}

// cmd builds a ConfigureProviderCommand with a fresh org/project for a kind.
func cmd(kind string, creds map[string]string) ConfigureProviderCommand {
	return ConfigureProviderCommand{
		ProjectID:   uuid.New(),
		OrgID:       uuid.New(),
		Kind:        kind,
		Credentials: creds,
	}
}

// validCreds returns a complete credential map for the given kind.
func validCreds(kind string) map[string]string {
	switch kind {
	case "slack":
		return map[string]string{"bot_token": "xoxb-test", "signing_secret": "super_secret"}
	case "teams":
		return map[string]string{
			"tenant_id":     "tenant-uuid",
			"client_id":     "client-uuid",
			"client_secret": "client-secret",
			"bot_app_id":    "bot-app-id",
		}
	case "telegram":
		return map[string]string{"bot_token": "123456:ABCDEF"}
	default:
		return nil
	}
}

// ── validation ────────────────────────────────────────────────────────────────

func TestConfigureProvider_RejectsUnknownKind(t *testing.T) {
	t.Parallel()

	h := newHandlerUnderTest(nil, nil, nil, nil)
	_, err := h.Handle(context.Background(), cmd("pigeon", map[string]string{"bot_token": "x"}))

	var inv errs.InvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("expected InvalidError, got %T: %v", err, err)
	}

	if inv.Field != "kind" {
		t.Errorf("InvalidError.Field = %q, want %q", inv.Field, "kind")
	}
}

func TestConfigureProvider_RejectsNilOrg(t *testing.T) {
	t.Parallel()

	h := newHandlerUnderTest(nil, nil, nil, nil)
	c := cmd("slack", validCreds("slack"))
	c.OrgID = uuid.Nil

	if _, err := h.Handle(context.Background(), c); err == nil {
		t.Fatal("expected error for nil org id")
	}
}

func TestConfigureProvider_RejectsMissingCredential_Slack(t *testing.T) {
	t.Parallel()

	h := newHandlerUnderTest(nil, nil, nil, nil)
	_, err := h.Handle(context.Background(), cmd("slack", map[string]string{"bot_token": "xoxb-test"}))

	var inv errs.InvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("expected InvalidError, got %T: %v", err, err)
	}

	if inv.Field != "credentials.signing_secret" {
		t.Errorf("InvalidError.Field = %q, want credentials.signing_secret", inv.Field)
	}
}

func TestConfigureProvider_RejectsMissingCredential_Telegram(t *testing.T) {
	t.Parallel()

	h := newHandlerUnderTest(nil, nil, nil, nil)
	_, err := h.Handle(context.Background(), cmd("telegram", map[string]string{"other": "x"}))

	var inv errs.InvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("expected InvalidError, got %T: %v", err, err)
	}

	if inv.Field != "credentials.bot_token" {
		t.Errorf("InvalidError.Field = %q, want credentials.bot_token", inv.Field)
	}
}

// ── happy paths: credentials land at the org level ────────────────────────────

func TestConfigureProvider_SlackHappyPath(t *testing.T) {
	t.Parallel()

	orgStore := &fakeOrgCredStore{}
	registry := &fakeProviderRefresher{}
	bus := &fakeEventBus{}
	h := newHandlerUnderTest(orgStore, nil, registry, bus)

	c := cmd("slack", validCreds("slack"))

	if _, err := h.Handle(context.Background(), c); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(orgStore.calls) != 1 {
		t.Fatalf("orgStore.UpsertProvider called %d times, want 1", len(orgStore.calls))
	}

	got := orgStore.calls[0]
	if got.orgID != c.OrgID {
		t.Errorf("orgStore.orgID = %v, want %v", got.orgID, c.OrgID)
	}

	if got.kind != "slack" {
		t.Errorf("orgStore.kind = %q, want slack", got.kind)
	}

	var stored map[string]string
	if err := json.Unmarshal(got.credentials, &stored); err != nil {
		t.Fatalf("stored credentials are not valid JSON: %v", err)
	}

	if stored["bot_token"] != "xoxb-test" {
		t.Errorf("stored bot_token = %q, want xoxb-test", stored["bot_token"])
	}

	env, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_PROVIDER_CONFIGURED)
	if !ok {
		t.Fatal("ProviderConfigured event not published")
	}

	if env.GetProviderConfigured().GetKind() != "slack" {
		t.Errorf("event.kind = %q, want slack", env.GetProviderConfigured().GetKind())
	}

	if len(registry.calls) != 1 {
		t.Fatalf("registry.RegisterFromMap called %d times, want 1", len(registry.calls))
	}
}

func TestConfigureProvider_CommunityInviteURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		kind    string
		invite  string
		wantErr bool
	}{
		{name: "slack https invite accepted", kind: kindSlack, invite: "https://acme.slack.com/join/abc", wantErr: false},
		{name: "discord https invite accepted", kind: kindDiscord, invite: "https://discord.gg/abc123", wantErr: false},
		{name: "empty invite accepted", kind: kindDiscord, invite: "", wantErr: false},
		{name: "relative invite rejected", kind: kindSlack, invite: "/join/abc", wantErr: true},
		{name: "non-http scheme rejected", kind: kindDiscord, invite: "javascript:alert(1)", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			orgStore := &fakeOrgCredStore{}
			h := newHandlerUnderTest(orgStore, nil, nil, nil)

			creds := validCreds(tc.kind)
			if creds == nil {
				creds = map[string]string{"bot_token": "discord-bot-token"} // discord requires only bot_token
			}

			creds["community_invite_url"] = tc.invite

			_, err := h.Handle(context.Background(), cmd(tc.kind, creds))

			if tc.wantErr {
				var inv errs.InvalidError
				if !errors.As(err, &inv) {
					t.Fatalf("expected InvalidError for invite %q, got %T: %v", tc.invite, err, err)
				}

				if inv.Field != "credentials.community_invite_url" {
					t.Errorf("Field = %q, want credentials.community_invite_url", inv.Field)
				}

				if len(orgStore.calls) != 0 {
					t.Errorf("org store must not be called on validation failure, got %d calls", len(orgStore.calls))
				}

				return
			}

			if err != nil {
				t.Fatalf("Handle() error = %v, want nil for invite %q", err, tc.invite)
			}

			if tc.invite == "" {
				return
			}

			var stored map[string]string
			if err := json.Unmarshal(orgStore.calls[0].credentials, &stored); err != nil {
				t.Fatalf("stored credentials not JSON: %v", err)
			}

			if stored["community_invite_url"] != tc.invite {
				t.Errorf("stored community_invite_url = %q, want %q", stored["community_invite_url"], tc.invite)
			}
		})
	}
}

func TestConfigureProvider_TeamsHappyPath(t *testing.T) {
	t.Parallel()

	orgStore := &fakeOrgCredStore{}
	h := newHandlerUnderTest(orgStore, nil, nil, nil)

	if _, err := h.Handle(context.Background(), cmd("teams", validCreds("teams"))); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(orgStore.calls) != 1 || orgStore.calls[0].kind != "teams" {
		t.Errorf("expected one org store call with kind=teams, got %v", orgStore.calls)
	}
}

func TestConfigureProvider_TeamsAcceptsOptionalServiceURL(t *testing.T) {
	t.Parallel()

	orgStore := &fakeOrgCredStore{}
	h := newHandlerUnderTest(orgStore, nil, nil, nil)

	creds := validCreds("teams")
	creds["service_url"] = "https://smba.trafficmanager.net/emea/"

	if _, err := h.Handle(context.Background(), cmd("teams", creds)); err != nil {
		t.Fatalf("Handle() error = %v, want nil (service_url is optional)", err)
	}

	if len(orgStore.calls) != 1 {
		t.Fatalf("expected one org store call, got %d", len(orgStore.calls))
	}

	var persisted map[string]string
	if err := json.Unmarshal(orgStore.calls[0].credentials, &persisted); err != nil {
		t.Fatalf("decoding persisted credentials: %v", err)
	}

	if persisted["service_url"] != creds["service_url"] {
		t.Errorf("service_url not persisted: got %q, want %q", persisted["service_url"], creds["service_url"])
	}
}

func TestConfigureProvider_RejectsUnsafeTeamsServiceURL(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, serviceURL string }{
		{"http_scheme", "http://smba.trafficmanager.net/emea/"},
		{"internal_ip", "https://10.0.0.5/webhook"},
		{"arbitrary_host", "https://evil.example.com/collect"},
		{"malformed", "://not-a-url"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			orgStore := &fakeOrgCredStore{}
			h := newHandlerUnderTest(orgStore, nil, nil, nil)

			creds := validCreds("teams")
			creds["service_url"] = tc.serviceURL

			_, err := h.Handle(context.Background(), cmd("teams", creds))

			var inv errs.InvalidError
			if !errors.As(err, &inv) {
				t.Fatalf("service_url %q: expected InvalidError, got %T: %v", tc.serviceURL, err, err)
			}

			if inv.Field != "credentials.service_url" {
				t.Errorf("service_url %q: Field = %q, want credentials.service_url", tc.serviceURL, inv.Field)
			}

			if len(orgStore.calls) != 0 {
				t.Errorf("service_url %q: org store must not be called on validation failure, got %d", tc.serviceURL, len(orgStore.calls))
			}
		})
	}
}

func TestConfigureProvider_AcceptsAllowlistedTeamsServiceURL(t *testing.T) {
	t.Parallel()

	for _, serviceURL := range []string{
		"https://smba.trafficmanager.net/emea/",
		"https://smba.botframework.com/emea/",
	} {
		t.Run(serviceURL, func(t *testing.T) {
			t.Parallel()

			orgStore := &fakeOrgCredStore{}
			h := newHandlerUnderTest(orgStore, nil, nil, nil)

			creds := validCreds("teams")
			creds["service_url"] = serviceURL

			if _, err := h.Handle(context.Background(), cmd("teams", creds)); err != nil {
				t.Fatalf("Handle() error = %v, want nil for allowlisted service_url %q", err, serviceURL)
			}

			if len(orgStore.calls) != 1 {
				t.Fatalf("expected one org store call, got %d", len(orgStore.calls))
			}
		})
	}
}

func TestConfigureProvider_TelegramHappyPath(t *testing.T) {
	t.Parallel()

	orgStore := &fakeOrgCredStore{}
	h := newHandlerUnderTest(orgStore, nil, nil, nil)

	if _, err := h.Handle(context.Background(), cmd("telegram", validCreds("telegram"))); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(orgStore.calls) != 1 || orgStore.calls[0].kind != "telegram" {
		t.Errorf("expected one org store call with kind=telegram, got %v", orgStore.calls)
	}
}

// ── alert channels: per-project override ──────────────────────────────────────

func TestConfigureProvider_WithChannelsWritesProjectOverride(t *testing.T) {
	t.Parallel()

	orgStore := &fakeOrgCredStore{}
	channels := &fakeProviderConfigStore{}
	h := newHandlerUnderTest(orgStore, channels, nil, nil)

	c := cmd("slack", validCreds("slack"))
	c.AlertChannelIDs = []string{"C123"}

	if _, err := h.Handle(context.Background(), c); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(orgStore.calls) != 1 {
		t.Errorf("org credentials not written: %d calls", len(orgStore.calls))
	}

	if len(channels.alertCalls) != 1 || channels.alertCalls[0].projectID != c.ProjectID {
		t.Errorf("project alert-channel override not written for the project: %v", channels.alertCalls)
	}
}

func TestConfigureProvider_EmptyCredentialsWritesOnlyChannels(t *testing.T) {
	t.Parallel()

	orgStore := &fakeOrgCredStore{}
	channels := &fakeProviderConfigStore{}
	h := newHandlerUnderTest(orgStore, channels, nil, nil)

	c := ConfigureProviderCommand{
		ProjectID:       uuid.New(),
		OrgID:           uuid.New(),
		Kind:            "slack",
		Credentials:     map[string]string{},
		AlertChannelIDs: []string{"C999"},
	}

	if _, err := h.Handle(context.Background(), c); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(orgStore.calls) != 0 {
		t.Errorf("empty-credentials path must not touch org credentials, got %d calls", len(orgStore.calls))
	}

	if len(channels.alertCalls) != 1 {
		t.Errorf("expected one project alert-channel write, got %d", len(channels.alertCalls))
	}
}

// ── event / error propagation ─────────────────────────────────────────────────

func TestConfigureProvider_EventCarriesNoCredentials(t *testing.T) {
	t.Parallel()

	bus := &fakeEventBus{}
	h := newHandlerUnderTest(nil, nil, nil, bus)

	if _, err := h.Handle(context.Background(), cmd("slack", validCreds("slack"))); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	env, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_PROVIDER_CONFIGURED)
	if !ok {
		t.Fatal("no ProviderConfigured event published")
	}

	pc := env.GetProviderConfigured()
	if pc.GetProjectId() == "" || pc.GetKind() == "" {
		t.Error("event missing project_id or kind")
	}
}

func TestConfigureProvider_StoreErrorPropagates(t *testing.T) {
	t.Parallel()

	orgStore := &fakeOrgCredStore{upsertErr: errors.New("db: connection reset")}
	bus := &fakeEventBus{}
	h := newHandlerUnderTest(orgStore, nil, nil, bus)

	if _, err := h.Handle(context.Background(), cmd("slack", validCreds("slack"))); err == nil {
		t.Fatal("expected error when store fails, got nil")
	}

	if len(bus.published) != 0 {
		t.Errorf("expected 0 events published when store fails, got %d", len(bus.published))
	}
}

func TestConfigureProvider_BusErrorPropagates(t *testing.T) {
	t.Parallel()

	bus := &fakeEventBus{publishErr: errors.New("bus: overflow")}
	registry := &fakeProviderRefresher{}
	h := newHandlerUnderTest(nil, nil, registry, bus)

	if _, err := h.Handle(context.Background(), cmd("slack", validCreds("slack"))); err == nil {
		t.Fatal("expected error when bus fails, got nil")
	}

	if len(registry.calls) != 0 {
		t.Errorf("expected 0 registry calls when bus fails, got %d", len(registry.calls))
	}
}

func TestConfigureProvider_RegistryErrorIsNonFatal(t *testing.T) {
	t.Parallel()

	registry := &fakeProviderRefresher{registerErr: errors.New("registry: unknown kind")}
	orgStore := &fakeOrgCredStore{}
	bus := &fakeEventBus{}
	h := newHandlerUnderTest(orgStore, nil, registry, bus)

	if _, err := h.Handle(context.Background(), cmd("slack", validCreds("slack"))); err != nil {
		t.Fatalf("registry errors must be non-fatal; got: %v", err)
	}

	if len(orgStore.calls) != 1 {
		t.Errorf("org store not called (calls = %d)", len(orgStore.calls))
	}

	if _, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_PROVIDER_CONFIGURED); !ok {
		t.Error("ProviderConfigured event not published despite registry failure")
	}
}

// ── partial-update merge ──────────────────────────────────────────────────────

// storedCreds unmarshals the credentials of the org store's single upsert call.
func storedCreds(t *testing.T, s *fakeOrgCredStore) map[string]string {
	t.Helper()

	if len(s.calls) != 1 {
		t.Fatalf("expected exactly one org credential upsert, got %d", len(s.calls))
	}

	var creds map[string]string
	if err := json.Unmarshal(s.calls[0].credentials, &creds); err != nil {
		t.Fatalf("stored credentials are not valid JSON: %v", err)
	}

	return creds
}

// A partial update (only the OAuth2 client secret) must merge over the existing
// connection so the bot_token an admin never re-typed is preserved.
func TestConfigureProvider_PartialUpdateMergesOverExisting(t *testing.T) {
	t.Parallel()

	existing, err := json.Marshal(map[string]string{"bot_token": "keep-me"})
	if err != nil {
		t.Fatalf("seed marshal: %v", err)
	}

	orgStore := &fakeOrgCredStore{loaded: existing}
	h := newHandlerUnderTest(orgStore, nil, nil, nil)

	if _, err := h.Handle(context.Background(), cmd("discord", map[string]string{"client_secret": "the-secret"})); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	got := storedCreds(t, orgStore)
	if got["bot_token"] != "keep-me" {
		t.Errorf("bot_token not preserved through partial update: %q", got["bot_token"])
	}

	if got["client_secret"] != "the-secret" {
		t.Errorf("client_secret not added: %q", got["client_secret"])
	}
}

// An empty provided value must not overwrite the stored one — it means "leave it".
func TestConfigureProvider_EmptyValueKeepsStored(t *testing.T) {
	t.Parallel()

	existing, err := json.Marshal(map[string]string{"bot_token": "keep-me", "client_secret": "old-secret"})
	if err != nil {
		t.Fatalf("seed marshal: %v", err)
	}

	orgStore := &fakeOrgCredStore{loaded: existing}
	h := newHandlerUnderTest(orgStore, nil, nil, nil)

	// bot_token blank (untouched field), client_secret rotated.
	if _, err := h.Handle(context.Background(), cmd("discord", map[string]string{"bot_token": "  ", "client_secret": "new-secret"})); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	got := storedCreds(t, orgStore)
	if got["bot_token"] != "keep-me" {
		t.Errorf("blank value wiped stored bot_token: %q", got["bot_token"])
	}

	if got["client_secret"] != "new-secret" {
		t.Errorf("client_secret not rotated: %q", got["client_secret"])
	}
}

// A genuine load failure must abort — never store a partial set that would wipe
// the org's other secrets.
func TestConfigureProvider_LoadFailureAbortsWithoutWipe(t *testing.T) {
	t.Parallel()

	orgStore := &fakeOrgCredStore{loadErr: errors.New("db: connection reset")}
	h := newHandlerUnderTest(orgStore, nil, nil, nil)

	if _, err := h.Handle(context.Background(), cmd("discord", map[string]string{"client_secret": "x"})); err == nil {
		t.Fatal("a load failure must abort, not silently store a partial set")
	}

	if len(orgStore.calls) != 0 {
		t.Errorf("nothing must be persisted on a load failure, got %d upserts", len(orgStore.calls))
	}
}

// A fresh connection (nothing stored yet) still requires every mandatory key —
// merge over an empty set must not weaken validation.
func TestConfigureProvider_FreshConnectionStillValidatesRequired(t *testing.T) {
	t.Parallel()

	orgStore := &fakeOrgCredStore{} // loaded empty → LoadProvider returns ErrNotFound
	h := newHandlerUnderTest(orgStore, nil, nil, nil)

	// discord requires bot_token; supplying only client_secret must fail.
	if _, err := h.Handle(context.Background(), cmd("discord", map[string]string{"client_secret": "x"})); err == nil {
		t.Fatal("a fresh connection missing bot_token must fail validation")
	}

	if len(orgStore.calls) != 0 {
		t.Errorf("nothing must be persisted when validation fails, got %d upserts", len(orgStore.calls))
	}
}
