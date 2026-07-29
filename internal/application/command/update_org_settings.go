// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// UpdateOrganizationSettingsCommand stores explicit escalation timeouts for
// the caller's organization. Zero disables the rule; values are seconds.
type UpdateOrganizationSettingsCommand struct {
	OrgID uuid.UUID
	// IsOrgAdmin gates the write: only org admins set escalation policy.
	IsOrgAdmin bool
	// ClaimTimeoutSeconds is the unanswered-nudge delay (wire name keeps the
	// legacy "claim" spelling); 0 disables the rule.
	ClaimTimeoutSeconds int64
	// AlertTimeoutSeconds is the channel-alert rung; 0 disables it. nil
	// (absent on the wire) leaves the stored value unchanged — presence
	// distinguishes "unset" from an explicit 0.
	AlertTimeoutSeconds *int64 `exhaustruct:"optional"`
	// DefaultAlertChannelID follows the "empty = unchanged, '-' = clear"
	// convention.
	DefaultAlertChannelID string `exhaustruct:"optional"`
}

// orgSettingsWriter is the write port. *conversation.EscalationStore satisfies it.
type orgSettingsWriter interface {
	UpdateSettings(ctx context.Context, orgID uuid.UUID, claimSeconds int64, alertSeconds *int64, defaultAlertChannelID string) error
}

// UpdateOrganizationSettingsHandler handles UpdateOrganizationSettingsCommand.
type UpdateOrganizationSettingsHandler struct {
	settings orgSettingsWriter
}

// MustNewUpdateOrganizationSettingsHandler builds a handler. It panics on a nil dependency.
func MustNewUpdateOrganizationSettingsHandler(settings orgSettingsWriter) UpdateOrganizationSettingsHandler {
	if settings == nil {
		panic("UpdateOrganizationSettingsHandler requires a non-nil orgSettingsWriter")
	}

	return UpdateOrganizationSettingsHandler{settings: settings}
}

// Handle validates and stores the escalation settings.
func (h UpdateOrganizationSettingsHandler) Handle(ctx context.Context, cmd UpdateOrganizationSettingsCommand) error {
	if !cmd.IsOrgAdmin {
		return errs.ForbiddenError{Action: "update organization settings"}
	}

	if cmd.OrgID == uuid.Nil {
		return errs.InvalidError{Field: fieldOrgID, Reason: reasonNoOrgResolved}
	}

	if cmd.ClaimTimeoutSeconds < 0 {
		return errs.InvalidError{Field: "claim_timeout_seconds", Reason: "must be zero (disabled) or positive"}
	}

	if cmd.AlertTimeoutSeconds != nil && *cmd.AlertTimeoutSeconds < 0 {
		return errs.InvalidError{Field: "alert_timeout_seconds", Reason: "must be zero (disabled) or positive"}
	}

	return h.settings.UpdateSettings(ctx, cmd.OrgID, cmd.ClaimTimeoutSeconds, cmd.AlertTimeoutSeconds, cmd.DefaultAlertChannelID)
}
