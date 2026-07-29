// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// GetOrganizationSettingsQuery reads the caller's org escalation settings.
type GetOrganizationSettingsQuery struct {
	OrgID uuid.UUID
	// IsOrgAdmin gates the read: escalation policy is an admin surface.
	IsOrgAdmin bool
}

// OrgSettingsView carries the EFFECTIVE values (defaults resolved) plus flags
// saying whether each is the product default rather than an explicit choice.
type OrgSettingsView struct {
	ClaimTimeoutSeconds   int64
	ClaimIsDefault        bool
	AlertTimeoutSeconds   int64
	AlertIsDefault        bool
	DefaultAlertChannelID string
}

// orgSettingsReader is the read port. *conversation.EscalationStore satisfies it.
type orgSettingsReader interface {
	ReadSettings(ctx context.Context, orgID uuid.UUID) (OrgSettingsView, error)
}

// GetOrganizationSettingsHandler handles GetOrganizationSettingsQuery.
type GetOrganizationSettingsHandler struct {
	settings orgSettingsReader
}

// MustNewGetOrganizationSettingsHandler builds a handler. It panics
// on a nil dependency.
func MustNewGetOrganizationSettingsHandler(settings orgSettingsReader) GetOrganizationSettingsHandler {
	if settings == nil {
		panic("GetOrganizationSettingsHandler requires a non-nil orgSettingsReader")
	}

	return GetOrganizationSettingsHandler{settings: settings}
}

// Handle resolves the effective escalation settings for the caller's org.
func (h GetOrganizationSettingsHandler) Handle(ctx context.Context, q GetOrganizationSettingsQuery) (OrgSettingsView, error) {
	if !q.IsOrgAdmin {
		return OrgSettingsView{}, errs.ForbiddenError{Action: "read organization settings"}
	}

	if q.OrgID == uuid.Nil {
		return OrgSettingsView{}, errs.InvalidError{Field: "org_id", Reason: "no organization resolved for the caller"}
	}

	settings, err := h.settings.ReadSettings(ctx, q.OrgID)
	if err != nil {
		return OrgSettingsView{}, translateReadError(err, "organization_settings")
	}

	return settings, nil
}
