// SPDX-License-Identifier: AGPL-3.0-or-later

package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/pkg/pgconv"
	postgres "github.com/orako-io/core/internal/pkg/postgres"
)

// SetAvailability flips a member between active and on_leave, storing the
// return_date reminder only for on_leave. Returns adaptererr.ErrNotFound when no
// eligible row was updated (e.g. the member is deactivated/removed).
func (s *MemberStore) SetAvailability(ctx context.Context, id uuid.UUID, status model.MemberStatus, returnDate *time.Time) error {
	n, err := New(postgres.Conn(ctx, s.pool)).setMemberAvailability(ctx, setMemberAvailabilityParams{
		ID:         id,
		Status:     string(status),
		ReturnDate: dateOrNull(returnDate),
	})
	if err != nil {
		return fmt.Errorf("setting member availability: %w", adaptererr.Decode(err))
	}

	if n == 0 {
		return adaptererr.ErrNotFound
	}

	return nil
}

// SetActivation deactivates or reactivates a member. Returns
// adaptererr.ErrNotFound when the member is removed/purged (never touched).
func (s *MemberStore) SetActivation(ctx context.Context, id uuid.UUID, status model.MemberStatus) error {
	n, err := New(postgres.Conn(ctx, s.pool)).setMemberActivation(ctx, setMemberActivationParams{
		ID:     id,
		Status: string(status),
	})
	if err != nil {
		return fmt.Errorf("setting member activation: %w", adaptererr.Decode(err))
	}

	if n == 0 {
		return adaptererr.ErrNotFound
	}

	return nil
}

// OffboardFromOrg revokes the member's links only in orgID. The query
// terminalizes the member and frees chat bindings only when no membership in
// another organization remains.
func (s *MemberStore) OffboardFromOrg(ctx context.Context, member model.Member, orgID uuid.UUID) error {
	n, err := New(postgres.Conn(ctx, s.pool)).offboardMember(ctx, offboardMemberParams{
		ID:          member.ID,
		Status:      string(member.Status),
		Email:       pgconv.TextOrNull(member.Email),
		DisplayName: pgconv.TextOrNull(member.DisplayName),
		OrgID:       pgconv.UUIDOrNull(orgID),
	})
	if err != nil {
		return fmt.Errorf("offboarding member: %w", adaptererr.Decode(err))
	}

	if n == 0 {
		return adaptererr.ErrNotFound
	}

	return nil
}

// ListOrgMembers returns the rich roster for an org as a query read model — the
// live members everyone sees (pending self-join redeemers excluded; those come
// from ListPendingOrgMembers).
func (s *MemberStore) ListOrgMembers(ctx context.Context, orgID uuid.UUID) ([]query.OrgMemberView, error) {
	return s.listRoster(ctx, orgID, false, "listing org members")
}

// ListPendingOrgMembers returns the org's self-join members awaiting approval
// (status 'pending') as query read models. These are deliberately absent from
// ListOrgMembers; the caller (server) restricts this to org admins.
func (s *MemberStore) ListPendingOrgMembers(ctx context.Context, orgID uuid.UUID) ([]query.OrgMemberView, error) {
	return s.listRoster(ctx, orgID, true, "listing pending org members")
}

// listRoster runs the shared roster query (one SQL body, split by pendingOnly)
// and maps each row to a read model. pendingOnly=false yields the everyone-sees
// roster; pendingOnly=true yields only the pending self-join members.
func (s *MemberStore) listRoster(ctx context.Context, orgID uuid.UUID, pendingOnly bool, action string) ([]query.OrgMemberView, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).listOrgRoster(ctx, listOrgRosterParams{
		OrgID:       pgconv.UUIDOrNull(orgID),
		PendingOnly: pendingOnly,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", action, adaptererr.Decode(err))
	}

	out := make([]query.OrgMemberView, 0, len(rows))
	for _, r := range rows {
		view, err := orgMemberRowToView(r)
		if err != nil {
			return nil, err
		}

		out = append(out, view)
	}

	return out, nil
}

// OrgMemberByID returns one member's rich record, scoped to the org.
func (s *MemberStore) OrgMemberByID(ctx context.Context, orgID, memberID uuid.UUID) (query.OrgMemberView, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).orgMemberByID(ctx, orgMemberByIDParams{OrgID: pgconv.UUIDOrNull(orgID), ID: memberID})
	if err != nil {
		return query.OrgMemberView{}, fmt.Errorf("fetching org member: %w", adaptererr.Decode(err))
	}

	return orgMemberRowToView(listOrgRosterRow(row))
}

// projectExpertiseJSON decodes the json_agg produced by the roster queries.
type projectExpertiseJSON struct {
	ProjectID string   `json:"project_id"`
	Domains   []string `json:"domains"`
}

func orgMemberRowToView(r listOrgRosterRow) (query.OrgMemberView, error) {
	var raw []projectExpertiseJSON
	if len(r.Projects) > 0 {
		if err := json.Unmarshal(r.Projects, &raw); err != nil {
			return query.OrgMemberView{}, fmt.Errorf("decoding member projects: %w", err)
		}
	}

	projects := make([]query.ProjectExpertise, 0, len(raw))
	for _, p := range raw {
		pid, err := uuid.Parse(p.ProjectID)
		if err != nil {
			return query.OrgMemberView{}, fmt.Errorf("parsing project id %q: %w", p.ProjectID, err)
		}

		domains := p.Domains
		if domains == nil {
			domains = []string{}
		}

		projects = append(projects, query.ProjectExpertise{ProjectID: pid, Domains: domains})
	}

	return query.OrgMemberView{
		MemberID:        r.ID,
		Email:           pgconv.StringFromText(r.Email),
		DisplayName:     pgconv.StringFromText(r.DisplayName),
		FirstName:       r.FirstName,
		LastName:        r.LastName,
		GitHandle:       r.GitHandle,
		SlackUserID:     r.SlackUserID,
		TelegramChatID:  r.TelegramChatID,
		TeamsUserID:     r.TeamsUserID,
		DiscordUserID:   r.DiscordUserID,
		BindingError:    r.BindingError,
		DeliveryChannel: r.DeliveryChannel,
		Status:          r.Status,
		ReturnDate:      timeFromDate(r.ReturnDate),
		IsOrgAdmin:      r.IsOrgAdmin,
		External:        r.External,
		Projects:        projects,
	}, nil
}

// dateOrNull maps an optional day to a pgtype.Date (NULL when nil).
func dateOrNull(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{}
	}

	return pgtype.Date{Time: *t, Valid: true}
}

// timeFromDate maps a pgtype.Date back to an optional day.
func timeFromDate(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}

	t := d.Time

	return &t
}

// AccountID returns the login account linked to a member (ok=false for an
// external-only member with no account).
func (s *MemberStore) AccountID(ctx context.Context, memberID uuid.UUID) (uuid.UUID, bool, error) {
	raw, err := New(postgres.Conn(ctx, s.pool)).memberAccountID(ctx, memberID)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("fetching member account id: %w", adaptererr.Decode(err))
	}

	if !raw.Valid {
		return uuid.Nil, false, nil
	}

	return raw.Bytes, true, nil
}
