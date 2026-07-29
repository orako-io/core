// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// DisconnectProviderCommand removes a project's messaging provider. Caller
// identity comes from the request context; admin-gating is enforced by the
// transport interceptor (same as ConfigureProvider).
type DisconnectProviderCommand struct {
	ProjectID uuid.UUID
	// Kind is the provider type to remove: "slack", "teams", "telegram", or "discord".
	Kind string
}

// DisconnectProviderResult is the (empty) result of DisconnectProvider.
type DisconnectProviderResult struct{}

// providerDeleter is the narrow port to remove a stored provider config.
type providerDeleter interface {
	DeleteProvider(ctx context.Context, projectID uuid.UUID, kind string) error
}

// providerUnregisterer drops the live in-memory provider so a removed provider
// stops delivering before the next restart. *provider.Registry satisfies it.
type providerUnregisterer interface {
	Unregister(projectID uuid.UUID, kind string)
}

// DisconnectProviderHandler deletes a provider's stored config and unregisters
// it from the live registry. Idempotent: removing an absent provider succeeds.
type DisconnectProviderHandler struct {
	store    providerDeleter
	registry providerUnregisterer
}

// MustNewDisconnectProviderHandler builds a handler. Panics on a nil
// dependency.
func MustNewDisconnectProviderHandler(store providerDeleter, registry providerUnregisterer) DisconnectProviderHandler {
	if store == nil {
		panic("DisconnectProviderHandler requires a non-nil store")
	}

	if registry == nil {
		panic("DisconnectProviderHandler requires a non-nil registry")
	}

	return DisconnectProviderHandler{store: store, registry: registry}
}

// Handle validates the kind, deletes the stored config, and unregisters the
// live provider.
func (h DisconnectProviderHandler) Handle(ctx context.Context, cmd DisconnectProviderCommand) (DisconnectProviderResult, error) {
	if _, ok := requiredCredentials[cmd.Kind]; !ok {
		return DisconnectProviderResult{}, errs.InvalidError{
			Field:  fieldKind,
			Reason: fmt.Sprintf("unknown provider kind %q; must be one of: slack, teams, telegram, discord", cmd.Kind),
		}
	}

	if err := h.store.DeleteProvider(ctx, cmd.ProjectID, cmd.Kind); err != nil {
		return DisconnectProviderResult{}, translateErr(err, "provider")
	}

	h.registry.Unregister(cmd.ProjectID, cmd.Kind)

	return DisconnectProviderResult{}, nil
}
