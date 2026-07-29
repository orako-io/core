// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"

	"github.com/google/uuid"
)

// ProviderConfigStore is the write-only domain port for persisting per-project
// messaging provider credentials. The integration.ProjectProviderStore satisfies
// this interface in production.
type ProviderConfigStore interface {
	// UpsertProvider persists the raw JSON credentials and alert channels for
	// projectID under kind. An existing entry for the same (projectID, kind)
	// pair is overwritten. alertChannelIDs may be empty (no project-level alert
	// channel; the org default, if any, is the fallback). On a credential
	// rotation (conflict), an empty list leaves the stored channels unchanged.
	UpsertProvider(ctx context.Context, projectID uuid.UUID, kind string, credentials []byte, alertChannelIDs []string) error
}

// ProviderConfigLoader is the read-only domain port for loading provider
// configs. Used by the registry read-through and startup hydration.
type ProviderConfigLoader interface {
	// LoadProvider returns the raw JSON credentials stored for the exact
	// (projectID, kind) pair. Returns adaptererr.ErrNotFound when that pair
	// has no stored row, even if the project has other kinds configured.
	LoadProvider(ctx context.Context, projectID uuid.UUID, kind string) (credentials []byte, err error)
}

// ProviderRow is one row from LoadAllProviders. It carries the project,
// kind, and raw credential JSON without unmarshalling them into a typed struct.
type ProviderRow struct {
	ProjectID   uuid.UUID
	Kind        string
	Credentials []byte
}

// AllProvidersLoader returns every configured project-provider pair. Used at
// startup to hydrate the in-memory registry so configured providers survive
// restarts.
type AllProvidersLoader interface {
	LoadAllProviders(ctx context.Context) ([]ProviderRow, error)
}

// ProviderAlertChannel is one configured provider kind for a project,
// alongside its stored alert channel (empty when none is set). It is the
// read-side counterpart to UpsertProvider's alertChannelID, needed so the
// dashboard can display and safely re-edit a project's configured alert
// channel instead of blind-resending credentials with the field blank.
//
// Wired onto ListConnectedChannelsResponse.connected (pkg v0.7.0's
// ConnectedChannel{kind, alert_channel_id}) via GetProviderAlertChannelsHandler
// and server.go's ListConnectedChannels handler.
type ProviderAlertChannel struct {
	Kind            string
	AlertChannelIDs []string
}

// ProviderAlertChannelsReader is the read-only domain port for a project's
// configured providers and their stored alert channels.
type ProviderAlertChannelsReader interface {
	// ConfiguredProvidersWithAlertChannel returns each configured provider kind
	// for projectID with its stored alert_channel_ids (empty when none is set).
	// Empty slice (not an error) when no provider is configured.
	ConfiguredProvidersWithAlertChannel(ctx context.Context, projectID uuid.UUID) ([]ProviderAlertChannel, error)
}
