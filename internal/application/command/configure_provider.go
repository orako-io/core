// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/pkg/errs"
)

// Provider kind strings used in credential validation and error messages.
const (
	kindSlack        = "slack"
	kindTeams        = "teams"
	kindTelegram     = "telegram"
	kindDiscord      = "discord"
	credBotToken     = "bot_token"
	credServiceURL   = "service_url"
	credClientSecret = "client_secret"
	// credCommunityInviteURL is an OPTIONAL Slack/Discord credential (never in
	// requiredCredentials): the admin-set invite URL to the org's community
	// server, surfaced to new members during onboarding. Validated as an
	// absolute http(s) URL when present.
	credCommunityInviteURL = "community_invite_url"
)

// teamsServiceURLHostSuffixes are the only Bot Framework connector host
// suffixes ConfigureProvider will persist for the optional Teams service_url
// credential. Left unvalidated, that value becomes the base URL the Teams
// adapter dials with authenticated server-side POST/PUT requests (create
// conversation, post/PUT activity) — an SSRF letting any org admin turn the
// server into an internal request proxy. Empty stays allowed (the adapter
// falls back to its built-in default).
// teamsServiceURLHostSuffixes are Microsoft-owned zones matched by suffix.
// trafficmanager.net is deliberately NOT here: it is a shared Azure namespace
// (any tenant can create *.trafficmanager.net), so the Bot Framework connector
// host is pinned exactly instead — see teamsServiceURLExactHosts.
var teamsServiceURLHostSuffixes = []string{".botframework.com"} //nolint:gochecknoglobals // compile-time allowlist; package-level by design

// teamsServiceURLExactHosts are connector hosts on shared namespaces, matched
// exactly. smba.trafficmanager.net is the Bot Framework's global connector
// (Traffic Manager routes regionally by URL path, not subdomain).
var teamsServiceURLExactHosts = []string{"smba.trafficmanager.net"} //nolint:gochecknoglobals // compile-time allowlist; package-level by design

// requiredCredentials lists the mandatory credential keys per provider kind.
var requiredCredentials = map[string][]string{ //nolint:gochecknoglobals // compile-time credential key manifest; package-level by design
	kindSlack:    {credBotToken, "signing_secret"},
	kindTeams:    {"tenant_id", "client_id", credClientSecret, "bot_app_id"},
	kindTelegram: {credBotToken},
	kindDiscord:  {credBotToken},
}

// ConfigureProviderCommand is the input for setting a project's messaging
// provider. Caller identity comes from the request context, never this struct.
type ConfigureProviderCommand struct {
	ProjectID uuid.UUID
	// OrgID owns the provider connection (credentials). Integrations are
	// org-level: one connection serves every project in the org.
	OrgID uuid.UUID
	// Kind is the provider type: "slack", "teams", "telegram", or "discord".
	Kind string
	// Credentials are provider-specific key/value pairs (see
	// requiredCredentials). They must NEVER be logged or emitted into events.
	Credentials map[string]string
	// AlertChannelIDs are the provider-native channels for escalation alerts on
	// this project — the sweep posts to each. Empty falls back to the
	// organization's default alert channel; both empty means no channel alerts.
	AlertChannelIDs []string `exhaustruct:"optional"`
}

// ConfigureProviderResult is the (currently empty) result of ConfigureProvider.
type ConfigureProviderResult struct{}

// orgCredStore is the write port for the org-level provider connection
// (credentials). Satisfied by *integration.OrgProviderStore.
type orgCredStore interface {
	UpsertProvider(ctx context.Context, orgID uuid.UUID, kind string, credentials []byte) error
	// LoadProvider returns the stored credential JSON, or adaptererr.ErrNotFound
	// when the org has no connection of that kind yet. Used to MERGE a partial
	// update over the existing set.
	LoadProvider(ctx context.Context, orgID uuid.UUID, kind string) ([]byte, error)
}

// providerConfigStore is the write port for the per-project alert-channel
// override (no credentials — those live at the org level).
type providerConfigStore interface {
	// UpsertAlertChannels inserts or updates the project's alert-channel override
	// for a kind, credential-free.
	UpsertAlertChannels(ctx context.Context, projectID uuid.UUID, kind string, alertChannelIDs []string) error
}

// providerRefresher refreshes the in-memory provider registry entry after
// credentials are persisted.
type providerRefresher interface {
	RegisterFromMap(projectID uuid.UUID, kind string, creds map[string]string) error
}

// ConfigureProviderHandler validates the kind and credential keys, persists
// credentials, emits a ProviderConfigured event (credentials excluded), and
// refreshes the live registry.
type ConfigureProviderHandler struct {
	orgStore orgCredStore
	channels providerConfigStore
	registry providerRefresher
	bus      eventBus
}

// MustNewConfigureProviderHandler builds a ConfigureProviderHandler. It panics on nil dependencies (same convention as other handlers).
func MustNewConfigureProviderHandler(
	orgStore orgCredStore,
	channels providerConfigStore,
	registry providerRefresher,
	bus eventBus,
) ConfigureProviderHandler {
	if orgStore == nil || channels == nil {
		panic("ConfigureProviderHandler requires non-nil org and channel stores")
	}

	if registry == nil {
		panic("ConfigureProviderHandler requires a non-nil providerRefresher")
	}

	if bus == nil {
		panic("ConfigureProviderHandler requires a non-nil eventBus")
	}

	return ConfigureProviderHandler{orgStore: orgStore, channels: channels, registry: registry, bus: bus}
}

// Handle validates, persists, events, and activates a provider configuration.
// The ProviderConfigured event carries project_id + kind but NO credentials.
func (h ConfigureProviderHandler) Handle(ctx context.Context, cmd ConfigureProviderCommand) (ConfigureProviderResult, error) {
	required, ok := requiredCredentials[cmd.Kind]
	if !ok {
		return ConfigureProviderResult{}, errs.InvalidError{
			Field:  fieldKind,
			Reason: fmt.Sprintf("unknown provider kind %q; must be one of: slack, teams, telegram, discord", cmd.Kind),
		}
	}

	if cmd.OrgID == uuid.Nil {
		return ConfigureProviderResult{}, errs.InvalidError{Field: fieldOrgID, Reason: reasonNilUUID}
	}

	// Empty credentials = set only this project's alert-channel override; the
	// org-level connection (credentials) is left untouched.
	if len(cmd.Credentials) == 0 {
		if err := h.channels.UpsertAlertChannels(ctx, cmd.ProjectID, cmd.Kind, cmd.AlertChannelIDs); err != nil {
			return ConfigureProviderResult{}, translateErr(err, "provider")
		}

		return ConfigureProviderResult{}, nil
	}

	// Merge over the existing org-level credentials so an admin can add or rotate
	// a SINGLE credential (e.g. the OAuth2 client secret) without re-entering the
	// others: a provided empty value keeps the stored one. A fresh connection has
	// no existing set, so the validation below still requires every mandatory key.
	merged, err := h.mergeCredentials(ctx, cmd.OrgID, cmd.Kind, cmd.Credentials)
	if err != nil {
		return ConfigureProviderResult{}, err
	}

	if err := validateCredentials(cmd.Kind, required, merged); err != nil {
		return ConfigureProviderResult{}, err
	}

	// Credentials go into the store only; NEVER into events or logs.
	credJSON, err := json.Marshal(merged)
	if err != nil {
		return ConfigureProviderResult{}, errs.InternalError{Err: fmt.Errorf("marshaling credentials: %w", err)}
	}

	// Credentials go to the org-level connection.
	if err := h.orgStore.UpsertProvider(ctx, cmd.OrgID, cmd.Kind, credJSON); err != nil {
		return ConfigureProviderResult{}, translateErr(err, "provider")
	}

	// A project alert-channel override, when supplied, is stored per project.
	if len(cmd.AlertChannelIDs) > 0 {
		if err := h.channels.UpsertAlertChannels(ctx, cmd.ProjectID, cmd.Kind, cmd.AlertChannelIDs); err != nil {
			return ConfigureProviderResult{}, translateErr(err, "provider")
		}
	}

	if _, err := h.bus.Publish(ctx, &orakov1.Envelope{
		ProjectId: cmd.ProjectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_PROVIDER_CONFIGURED,
		Payload: &orakov1.Envelope_ProviderConfigured{
			ProviderConfigured: &orakov1.ProviderConfigured{
				ProjectId: cmd.ProjectID.String(),
				Kind:      cmd.Kind,
			},
		},
	}); err != nil {
		return ConfigureProviderResult{}, errs.InternalError{Err: fmt.Errorf("publishing ProviderConfigured event: %w", err)}
	}

	// A failed registry refresh is not fatal: the persisted config is loaded by
	// read-through on the next ForProject call.
	if err := h.registry.RegisterFromMap(cmd.ProjectID, cmd.Kind, merged); err != nil {
		slog.WarnContext(ctx, "provider registry refresh failed after ConfigureProvider; read-through will recover",
			slog.String("project_id", cmd.ProjectID.String()),
			slog.String("kind", cmd.Kind),
			slog.String("error", err.Error()),
		)
	}

	return ConfigureProviderResult{}, nil
}

// mergeCredentials overlays the provided non-empty credential values on top of
// the org's existing stored set, so a partial update (e.g. adding just the
// OAuth2 client secret) preserves the others. A genuine load failure aborts —
// it must never silently drop the existing secrets — while "not configured yet"
// starts from an empty set (a fresh connection).
func (h ConfigureProviderHandler) mergeCredentials(ctx context.Context, orgID uuid.UUID, kind string, provided map[string]string) (map[string]string, error) {
	merged := map[string]string{}

	raw, err := h.orgStore.LoadProvider(ctx, orgID, kind)
	switch {
	case err == nil:
		if len(raw) > 0 {
			if uerr := json.Unmarshal(raw, &merged); uerr != nil {
				return nil, errs.InternalError{Err: fmt.Errorf("reading existing credentials: %w", uerr)}
			}
		}
	case errors.Is(err, adaptererr.ErrNotFound):
		// No existing connection — a fresh set; validation requires every key.
	default:
		return nil, translateErr(err, "provider")
	}

	for k, v := range provided {
		if strings.TrimSpace(v) != "" {
			merged[k] = v
		}
	}

	return merged, nil
}

// validateCredentials checks that every required key is present and, for Teams,
// that the optional service_url is an allowlisted host.
func validateCredentials(kind string, required []string, creds map[string]string) error {
	for _, key := range required {
		if creds[key] == "" {
			return errs.InvalidError{
				Field:  "credentials." + key,
				Reason: fmt.Sprintf("required credential key %q is missing or empty for kind %q", key, kind),
			}
		}
	}

	if kind == kindTeams {
		return validateTeamsServiceURL(creds[credServiceURL])
	}

	if kind == kindSlack || kind == kindDiscord {
		return validateCommunityInviteURL(creds[credCommunityInviteURL])
	}

	return nil
}

// validateCommunityInviteURL enforces the optional community_invite_url is
// either empty (feature simply off) or an absolute http(s) URL. It is shown to
// new members and opened in their browser, so a relative or non-http scheme is
// rejected rather than persisted.
func validateCommunityInviteURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errs.InvalidError{
			Field:  "credentials." + credCommunityInviteURL,
			Reason: "must be an absolute http(s) URL",
		}
	}

	return nil
}

// validateTeamsServiceURL enforces the optional service_url credential is
// either empty (falls back to the adapter's built-in default) or an https URL
// whose host ends in an allowlisted Bot Framework connector suffix.
func validateTeamsServiceURL(raw string) error {
	if raw == "" {
		return nil
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return errs.InvalidError{
			Field:  "credentials." + credServiceURL,
			Reason: "must be an https URL",
		}
	}

	host := parsed.Hostname()

	if slices.Contains(teamsServiceURLExactHosts, host) {
		return nil
	}

	for _, suffix := range teamsServiceURLHostSuffixes {
		if strings.HasSuffix(host, suffix) {
			return nil
		}
	}

	return errs.InvalidError{
		Field:  "credentials." + credServiceURL,
		Reason: fmt.Sprintf("host %q is not an allowlisted Bot Framework connector host (must be smba.trafficmanager.net or end in .botframework.com)", host),
	}
}
