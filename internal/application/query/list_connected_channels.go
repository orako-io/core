// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// ListConnectedChannelsQuery is the input for listing a project's selectable
// delivery channels.
type ListConnectedChannelsQuery struct {
	// ProjectID scopes the provider lookup (the caller's primary project).
	ProjectID uuid.UUID
}

// ListConnectedChannelsHandler handles ListConnectedChannelsQuery.
type ListConnectedChannelsHandler struct {
	reader ConfiguredChannelsReader
}

// MustNewListConnectedChannelsHandler builds a handler. It panics on
// nil dependencies.
func MustNewListConnectedChannelsHandler(reader ConfiguredChannelsReader) ListConnectedChannelsHandler {
	if reader == nil {
		panic("ListConnectedChannelsHandler requires a non-nil ConfiguredChannelsReader")
	}

	return ListConnectedChannelsHandler{reader: reader}
}

// Handle returns the delivery channels the caller may select for the project:
// always the dashboard channel, plus each functional external provider that is
// configured. Slack, Teams, Telegram, and Discord are all real adapters as of
// phase 4 — every configured kind is offered.
func (h ListConnectedChannelsHandler) Handle(ctx context.Context, q ListConnectedChannelsQuery) ([]string, error) {
	kinds, err := h.reader.ConfiguredKinds(ctx, q.ProjectID)
	if err != nil {
		return nil, err
	}

	// The dashboard channel is always available and needs no configuration.
	channels := []string{string(model.DeliveryChannelDashboard)}

	for _, kind := range kinds {
		switch model.DeliveryChannel(kind) {
		case model.DeliveryChannelSlack, model.DeliveryChannelTelegram, model.DeliveryChannelTeams, model.DeliveryChannelDiscord:
			channels = append(channels, kind)
		case model.DeliveryChannelDashboard:
			// already included unconditionally above.
		}
	}

	return channels, nil
}
