// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/service"
)

// GetProviderAlertChannelsQuery is the input for reading a project's
// configured providers and their stored alert channels.
type GetProviderAlertChannelsQuery struct {
	// ProjectID scopes the provider lookup (the caller's primary project).
	ProjectID uuid.UUID
}

// GetProviderAlertChannelsHandler handles GetProviderAlertChannelsQuery. Wired
// onto ListConnectedChannels (server.go), which populates the `connected`
// field from this handler's result so the dashboard can prefill each
// provider's alert-channel input.
type GetProviderAlertChannelsHandler struct {
	reader service.ProviderAlertChannelsReader
}

// MustNewGetProviderAlertChannelsHandler builds a handler. It panics on a nil dependency.
func MustNewGetProviderAlertChannelsHandler(reader service.ProviderAlertChannelsReader) GetProviderAlertChannelsHandler {
	if reader == nil {
		panic("GetProviderAlertChannelsHandler requires a non-nil ProviderAlertChannelsReader")
	}

	return GetProviderAlertChannelsHandler{reader: reader}
}

// Handle returns the project's configured providers with their stored alert
// channel IDs (empty when none is set for that provider).
func (h GetProviderAlertChannelsHandler) Handle(ctx context.Context, q GetProviderAlertChannelsQuery) ([]service.ProviderAlertChannel, error) {
	return h.reader.ConfiguredProvidersWithAlertChannel(ctx, q.ProjectID)
}
