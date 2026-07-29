// SPDX-License-Identifier: AGPL-3.0-or-later

package conversation

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/query"
	postgres "github.com/orako-io/core/internal/pkg/postgres"
)

// EscalationStore backs the escalation sweeper (nudge + channel alert) and
// the per-organization escalation settings.
type EscalationStore struct {
	pool *pgxpool.Pool
}

// NewEscalationStore builds an EscalationStore backed by pool.
func NewEscalationStore(pool *pgxpool.Pool) *EscalationStore {
	return &EscalationStore{pool: pool}
}

// ReadSettings returns the organization's effective escalation settings as a
// query read model (defaults resolved, with is-default flags). The stored knobs
// (nil = default) are resolved here rather than in the handler so the read side
// never touches the domain aggregate.
func (s *EscalationStore) ReadSettings(ctx context.Context, orgID uuid.UUID) (query.OrgSettingsView, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).orgEscalationSettings(ctx, orgID)
	if err != nil {
		return query.OrgSettingsView{}, fmt.Errorf("reading org escalation settings: %w", adaptererr.Decode(err))
	}

	settings := model.OrgEscalationSettings{ //nolint:exhaustruct // pointer fields set below only when stored
		DefaultAlertChannelID: row.DefaultAlertChannelID,
	}

	if row.ClaimTimeoutSeconds.Valid {
		v := row.ClaimTimeoutSeconds.Int64
		settings.ClaimTimeoutSeconds = &v
	}

	if row.AlertTimeoutSeconds.Valid {
		v := row.AlertTimeoutSeconds.Int64
		settings.AlertTimeoutSeconds = &v
	}

	return query.OrgSettingsView{
		ClaimTimeoutSeconds:   settings.EffectiveClaimTimeoutSeconds(),
		ClaimIsDefault:        settings.ClaimTimeoutSeconds == nil,
		AlertTimeoutSeconds:   settings.EffectiveAlertTimeoutSeconds(),
		AlertIsDefault:        settings.AlertTimeoutSeconds == nil,
		DefaultAlertChannelID: settings.DefaultAlertChannelID,
	}, nil
}

// UpdateSettings stores explicit escalation values for the organization.
// defaultAlertChannelID follows the "empty = unchanged, '-' = clear"
// convention (see UpdateOrganizationSettingsRequest). alertSeconds is
// presence-based: nil leaves the stored alert_timeout_seconds unchanged
// (an absent optional field must never silently disable alerts); a non-nil
// value, including a pointer to 0, overwrites it (0 disables the rule).
func (s *EscalationStore) UpdateSettings(ctx context.Context, orgID uuid.UUID, claimSeconds int64, alertSeconds *int64, defaultAlertChannelID string) error {
	var alert pgtype.Int8
	if alertSeconds != nil {
		alert = pgtype.Int8{Int64: *alertSeconds, Valid: true}
	}

	if err := New(postgres.Conn(ctx, s.pool)).updateOrgEscalationSettings(ctx, updateOrgEscalationSettingsParams{
		ID:                  orgID,
		ClaimTimeoutSeconds: pgtype.Int8{Int64: claimSeconds, Valid: true},
		AlertTimeoutSeconds: alert,
		Column4:             defaultAlertChannelID,
	}); err != nil {
		return fmt.Errorf("updating org escalation settings: %w", adaptererr.Decode(err))
	}

	return nil
}

// UnclaimedForNudge lists pool conversations past their org's nudge timeout
// (stored column keeps the legacy claim_timeout_seconds name) that were never
// nudged. defaultSeconds fills in for orgs without a stored
// value.
func (s *EscalationStore) UnclaimedForNudge(ctx context.Context, defaultSeconds int64) ([]model.Conversation, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).unclaimedForNudge(ctx, pgtype.Int8{Int64: defaultSeconds, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("listing unclaimed conversations for nudge: %w", adaptererr.Decode(err))
	}

	conversations := make([]model.Conversation, len(rows))
	for i, row := range rows {
		conversations[i] = model.Conversation{ //nolint:exhaustruct // sweep view: only the fields the sweeper needs.
			ID:            row.ID,
			ProjectID:     row.ProjectID,
			AskerMemberID: row.AskerMemberID,
			Question:      row.Question,
			Context:       row.Context,
			CreatedAt:     row.CreatedAt,
		}
	}

	return conversations, nil
}

// MarkNudged flips the once-only nudge marker; false means another sweep
// already nudged this conversation.
func (s *EscalationStore) MarkNudged(ctx context.Context, conversationID uuid.UUID) (bool, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).markConversationNudged(ctx, conversationID)
	if err != nil {
		return false, fmt.Errorf("marking conversation nudged: %w", adaptererr.Decode(err))
	}

	return rows > 0, nil
}

// UnclaimedForAlert lists pool conversations past their org's alert timeout
// that have not yet been posted to the alert channel. defaultSeconds fills in
// for orgs without a stored value; a resolved 0 disables the rule.
func (s *EscalationStore) UnclaimedForAlert(ctx context.Context, defaultSeconds int64) ([]model.AlertCandidate, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).unclaimedForAlert(ctx, pgtype.Int8{Int64: defaultSeconds, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("listing unclaimed conversations for alert: %w", adaptererr.Decode(err))
	}

	candidates := make([]model.AlertCandidate, len(rows))
	for i, row := range rows {
		candidates[i] = model.AlertCandidate{
			ConversationID:         row.ID,
			ProjectID:              row.ProjectID,
			Question:               row.Question,
			Title:                  row.Title,
			CreatedAt:              row.CreatedAt,
			ProjectAlertChannelIDs: row.ProjectAlertChannelIds,
			ProjectAlertKind:       row.ProjectAlertKind,
			OrgAlertChannelID:      row.OrgAlertChannelID,
			CandidateCount:         row.CandidateCount,
		}
	}

	return candidates, nil
}

// MarkAlerted flips the once-only alert marker by compare-and-set; false
// means another sweep already claimed the right to post.
func (s *EscalationStore) MarkAlerted(ctx context.Context, conversationID uuid.UUID) (bool, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).markConversationAlerted(ctx, conversationID)
	if err != nil {
		return false, fmt.Errorf("marking conversation alerted: %w", adaptererr.Decode(err))
	}

	return rows > 0, nil
}

// UnclaimedForExpiry lists pool conversations that sat open with no responder
// past the expiry timeout — the org's effective alert timeout scaled by
// model.ExpiryAlertMultiplier. defaultAlertSeconds fills in for orgs without a
// stored alert value; a resolved alert timeout of 0 disables expiry for that org
// (the query drops it), so a shop that turned alerts off never auto-expires.
func (s *EscalationStore) UnclaimedForExpiry(ctx context.Context, defaultAlertSeconds int64) ([]model.Conversation, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).unclaimedForExpiry(ctx, unclaimedForExpiryParams{
		AlertTimeoutSeconds:   pgtype.Int8{Int64: defaultAlertSeconds, Valid: true},
		AlertTimeoutSeconds_2: pgtype.Int8{Int64: model.ExpiryAlertMultiplier, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("listing unclaimed conversations for expiry: %w", adaptererr.Decode(err))
	}

	conversations := make([]model.Conversation, len(rows))
	for i, row := range rows {
		conversations[i] = model.Conversation{ //nolint:exhaustruct // sweep view: only the fields the sweeper needs.
			ID:            row.ID,
			ProjectID:     row.ProjectID,
			AskerMemberID: row.AskerMemberID,
			Question:      row.Question,
			Context:       row.Context,
			CreatedAt:     row.CreatedAt,
		}
	}

	return conversations, nil
}

// MarkExpired transitions an open, still-unassigned pool conversation to
// timed_out by compare-and-set; false means the transition did not apply — a
// concurrent sweep already expired it, or a late answer stamped a responder /
// moved the status off 'open' first (the CAS never races a real answer).
func (s *EscalationStore) MarkExpired(ctx context.Context, conversationID uuid.UUID) (bool, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).markConversationExpired(ctx, conversationID)
	if err != nil {
		return false, fmt.Errorf("marking conversation timed_out: %w", adaptererr.Decode(err))
	}

	return rows > 0, nil
}
