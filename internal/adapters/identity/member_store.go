// SPDX-License-Identifier: AGPL-3.0-or-later

package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/pkg/pgconv"
	postgres "github.com/orako-io/core/internal/pkg/postgres"
)

// Compile-time assertion that MemberStore satisfies the MemberRepository port.
var _ repository.MemberRepository = (*MemberStore)(nil)

// MemberStore is the Postgres-backed MemberRepository.
type MemberStore struct {
	pool *pgxpool.Pool
}

// NewMemberStore builds a MemberStore backed by pool.
func NewMemberStore(pool *pgxpool.Pool) *MemberStore {
	return &MemberStore{pool: pool}
}

// Create durably stores a new member.
// Returns adaptererr.ErrDuplicate when a member with the same ID already exists.
func (s *MemberStore) Create(ctx context.Context, member model.Member) error {
	_, err := New(postgres.Conn(ctx, s.pool)).createMember(ctx, createMemberParams{
		ID:              member.ID,
		Email:           pgconv.TextOrNull(member.Email),
		DisplayName:     pgconv.TextOrNull(member.DisplayName),
		SlackUserID:     member.SlackUserID,
		TelegramChatID:  member.TelegramChatID,
		DeliveryChannel: deliveryChannelOrDefault(member.DeliveryChannel),
		Status:          string(member.Status),
	})
	if err != nil {
		return fmt.Errorf("creating member: %w", adaptererr.Decode(err))
	}

	return nil
}

// ByID fetches a member by its UUID.
// Returns adaptererr.ErrNotFound when no member with that ID exists.
func (s *MemberStore) ByID(ctx context.Context, id uuid.UUID) (model.Member, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).memberByID(ctx, id)
	if err != nil {
		return model.Member{}, fmt.Errorf("fetching member by id: %w", adaptererr.Decode(err))
	}

	return memberRowToModel(row), nil
}

// ReadMember returns the member as a query.MemberView read model, so the read
// side never depends on the domain aggregate. Returns adaptererr.ErrNotFound
// when absent.
func (s *MemberStore) ReadMember(ctx context.Context, id uuid.UUID) (query.MemberView, error) {
	member, err := s.ByID(ctx, id)
	if err != nil {
		return query.MemberView{}, err
	}

	return query.NewMemberView(member), nil
}

// BySlackUserID fetches a member by their Slack user ID.
// Returns adaptererr.ErrNotFound when no member with that Slack user ID exists,
// and when slackUserID is empty (the column is NOT NULL DEFAULT ”, so an empty
// lookup would otherwise match an arbitrary unbound member — same caveat as
// ByTeamsUserID/ByDiscordUserID).
func (s *MemberStore) BySlackUserID(ctx context.Context, slackUserID string) (model.Member, error) {
	if slackUserID == "" {
		return model.Member{}, fmt.Errorf("fetching member by slack user id: %w", adaptererr.ErrNotFound)
	}

	row, err := New(postgres.Conn(ctx, s.pool)).memberBySlackUserID(ctx, slackUserID)
	if err != nil {
		return model.Member{}, fmt.Errorf("fetching member by slack user id: %w", adaptererr.Decode(err))
	}

	return memberRowToModel(memberByIDRow(row)), nil
}

// ByTelegramChatID fetches a member by their Telegram chat ID.
// Returns adaptererr.ErrNotFound when no member with that Telegram chat ID
// exists, and when telegramChatID is empty (same empty-string caveat as
// BySlackUserID).
func (s *MemberStore) ByTelegramChatID(ctx context.Context, telegramChatID string) (model.Member, error) {
	if telegramChatID == "" {
		return model.Member{}, fmt.Errorf("fetching member by telegram chat id: %w", adaptererr.ErrNotFound)
	}

	row, err := New(postgres.Conn(ctx, s.pool)).memberByTelegramChatID(ctx, telegramChatID)
	if err != nil {
		return model.Member{}, fmt.Errorf("fetching member by telegram chat id: %w", adaptererr.Decode(err))
	}

	return memberRowToModel(memberByIDRow(row)), nil
}

// ByTeamsUserID fetches a member by their Microsoft Teams AAD object id
// (from.aadObjectId on an inbound Bot Framework Activity).
// Returns adaptererr.ErrNotFound when no member with that id exists, and when
// teamsUserID is empty (the column is NOT NULL DEFAULT ”, so an empty lookup
// would otherwise match an arbitrary unbound member).
func (s *MemberStore) ByTeamsUserID(ctx context.Context, teamsUserID string) (model.Member, error) {
	if teamsUserID == "" {
		return model.Member{}, fmt.Errorf("fetching member by teams user id: %w", adaptererr.ErrNotFound)
	}

	row, err := New(postgres.Conn(ctx, s.pool)).memberByTeamsUserID(ctx, teamsUserID)
	if err != nil {
		return model.Member{}, fmt.Errorf("fetching member by teams user id: %w", adaptererr.Decode(err))
	}

	return memberRowToModel(memberByIDRow(row)), nil
}

// ByDiscordUserID fetches a member by their Discord user snowflake.
// Returns adaptererr.ErrNotFound when no member with that id exists, and when
// discordUserID is empty (same empty-string caveat as ByTeamsUserID).
func (s *MemberStore) ByDiscordUserID(ctx context.Context, discordUserID string) (model.Member, error) {
	if discordUserID == "" {
		return model.Member{}, fmt.Errorf("fetching member by discord user id: %w", adaptererr.ErrNotFound)
	}

	row, err := New(postgres.Conn(ctx, s.pool)).memberByDiscordUserID(ctx, discordUserID)
	if err != nil {
		return model.Member{}, fmt.Errorf("fetching member by discord user id: %w", adaptererr.Decode(err))
	}

	return memberRowToModel(memberByIDRow(row)), nil
}

// ByEmail fetches a member by their email address.
// Returns adaptererr.ErrNotFound when no member with that email exists.
func (s *MemberStore) ByEmail(ctx context.Context, email string) (model.Member, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).memberByEmail(ctx, pgconv.TextOrNull(email))
	if err != nil {
		return model.Member{}, fmt.Errorf("fetching member by email: %w", adaptererr.Decode(err))
	}

	return memberRowToModel(memberByIDRow(row)), nil
}

// ByAccountID fetches the primary member linked to a human account. account_id
// is nullable and NON-unique (an account that creates several orgs owns several
// member rows), so the underlying query returns the earliest row deterministically.
// Returns adaptererr.ErrNotFound when the account owns no member.
func (s *MemberStore) ByAccountID(ctx context.Context, accountID uuid.UUID) (model.Member, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).memberByAccountID(ctx, pgconv.UUIDOrNull(accountID))
	if err != nil {
		return model.Member{}, fmt.Errorf("fetching member by account id: %w", adaptererr.Decode(err))
	}

	return memberRowToModel(memberByIDRow(row)), nil
}

// ByAccountInOrg fetches the account's primary member scoped to ONE org, resolved
// via the member's project memberships (project_members → projects.org_id). A live
// member wins over a dead one, then the most-recent breaks ties. Backs the
// org-scoped redeem idempotency guard, so an account that is a live member of org
// A can still redeem a join code for org B. Returns adaptererr.ErrNotFound when
// the account owns no member participating in that org.
func (s *MemberStore) ByAccountInOrg(ctx context.Context, accountID, orgID uuid.UUID) (model.Member, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).memberByAccountInOrg(ctx, memberByAccountInOrgParams{
		AccountID: pgconv.UUIDOrNull(accountID),
		OrgID:     pgconv.UUIDOrNull(orgID),
	})
	if err != nil {
		return model.Member{}, fmt.Errorf("fetching member by account in org: %w", adaptererr.Decode(err))
	}

	return memberRowToModel(memberByIDRow(row)), nil
}

// Update persists mutations to an existing member.
// Returns adaptererr.ErrNotFound when no member with that ID exists.
func (s *MemberStore) Update(ctx context.Context, member model.Member) error {
	_, err := New(postgres.Conn(ctx, s.pool)).updateMember(ctx, updateMemberParams{
		ID:              member.ID,
		DisplayName:     pgconv.TextOrNull(member.DisplayName),
		SlackUserID:     member.SlackUserID,
		TelegramChatID:  member.TelegramChatID,
		DeliveryChannel: deliveryChannelOrDefault(member.DeliveryChannel),
		Status:          string(member.Status),
		Email:           pgconv.TextOrNull(member.Email),
		FirstName:       member.FirstName,
		LastName:        member.LastName,
		GitHandle:       member.GitHandle,
		AccountID:       pgconv.UUIDOrNull(member.AccountID),
		TeamsUserID:     member.TeamsUserID,
		DiscordUserID:   member.DiscordUserID,
		BindingError:    member.BindingError,
	})
	if err != nil {
		return fmt.Errorf("updating member: %w", adaptererr.Decode(err))
	}

	return nil
}

// OnboardingDismissed reports whether the member has permanently dismissed the
// Get-started onboarding. Returns adaptererr.ErrNotFound when no member with
// that ID exists.
func (s *MemberStore) OnboardingDismissed(ctx context.Context, id uuid.UUID) (bool, error) {
	dismissed, err := New(postgres.Conn(ctx, s.pool)).memberOnboardingDismissed(ctx, id)
	if err != nil {
		return false, fmt.Errorf("reading member onboarding dismissal: %w", adaptererr.Decode(err))
	}

	return dismissed, nil
}

// SetOnboardingDismissed marks (or, with dismissed=false, un-marks) the member's
// Get-started onboarding as permanently dismissed. Returns adaptererr.ErrNotFound
// when no member with that ID exists.
func (s *MemberStore) SetOnboardingDismissed(ctx context.Context, id uuid.UUID, dismissed bool) error {
	q := New(postgres.Conn(ctx, s.pool))

	var (
		rows int64
		err  error
	)

	if dismissed {
		rows, err = q.setMemberOnboardingDismissed(ctx, id)
	} else {
		rows, err = q.clearMemberOnboardingDismissed(ctx, id)
	}

	if err != nil {
		return fmt.Errorf("setting member onboarding dismissal: %w", adaptererr.Decode(err))
	}

	if rows == 0 {
		return fmt.Errorf("setting member onboarding dismissal: %w", adaptererr.ErrNotFound)
	}

	return nil
}

// MembersMissingSlackBinding lists the project's members that have an email but
// no Slack binding yet — the backfill set for SyncChatDirectory.
func (s *MemberStore) MembersMissingSlackBinding(ctx context.Context, projectID uuid.UUID) ([]repository.MemberEmail, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).projectMembersMissingSlackBinding(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("listing members missing slack binding: %w", adaptererr.Decode(err))
	}

	out := make([]repository.MemberEmail, 0, len(rows))
	for _, r := range rows {
		out = append(out, repository.MemberEmail{ID: r.ID, Email: r.Email.String})
	}

	return out, nil
}

// memberRowToModel maps a memberByIDRow to a domain Member.
// All other per-query member row types are structurally identical to
// memberByIDRow and may be cast to it before calling this function.
func memberRowToModel(r memberByIDRow) model.Member {
	return model.Member{
		ID:              r.ID,
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
		DeliveryChannel: model.DeliveryChannel(r.DeliveryChannel),
		Status:          model.MemberStatus(r.Status),
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}
}

// CreateWithAccount creates a member linked to a human account (the org
// creator: active, dashboard-channel). Runs against the ambient transaction when
// the caller opened one, so it composes atomically with the rest of org creation.
func (s *MemberStore) CreateWithAccount(ctx context.Context, member model.Member) error {
	if err := New(postgres.Conn(ctx, s.pool)).createMemberWithAccount(ctx, createMemberWithAccountParams{
		ID:              member.ID,
		DisplayName:     pgconv.TextOrNull(member.DisplayName),
		DeliveryChannel: deliveryChannelOrDefault(member.DeliveryChannel),
		Status:          string(member.Status),
		AccountID:       pgconv.UUIDOrNull(member.AccountID),
		Email:           pgconv.TextOrNull(member.Email),
	}); err != nil {
		return fmt.Errorf("creating member with account: %w", adaptererr.Decode(err))
	}

	return nil
}

// FindOrCreateByEmail resolves the member for email, creating it when absent and
// reactivating a soft-removed match (its status flips back to invited). freshInvite
// is true when the member was newly created or reactivated — the invitation
// lifecycle must then run. Runs against the ambient transaction when the caller
// opened one, so it composes atomically with AddToProject.
func (s *MemberStore) FindOrCreateByEmail(ctx context.Context, email string, newMember model.Member) (memberID uuid.UUID, freshInvite bool, err error) {
	q := New(postgres.Conn(ctx, s.pool))

	row, lookupErr := q.memberByEmail(ctx, pgconv.TextOrNull(email))
	if lookupErr == nil {
		if row.Status != string(model.MemberStatusRemoved) {
			return row.ID, false, nil
		}

		rows, reErr := q.reactivateRemovedMember(ctx, row.ID)
		if reErr != nil {
			return uuid.Nil, false, fmt.Errorf("reactivating removed member: %w", adaptererr.Decode(reErr))
		}

		return row.ID, rows > 0, nil
	}

	if !errors.Is(adaptererr.Decode(lookupErr), adaptererr.ErrNotFound) {
		return uuid.Nil, false, fmt.Errorf("looking up member by email: %w", adaptererr.Decode(lookupErr))
	}

	created, createErr := q.createMember(ctx, createMemberParams{
		ID:              newMember.ID,
		Email:           pgconv.TextOrNull(newMember.Email),
		DisplayName:     pgconv.TextOrNull(newMember.DisplayName),
		SlackUserID:     newMember.SlackUserID,
		TelegramChatID:  newMember.TelegramChatID,
		DeliveryChannel: deliveryChannelOrDefault(newMember.DeliveryChannel),
		Status:          string(newMember.Status),
	})
	if createErr != nil {
		return uuid.Nil, false, fmt.Errorf("creating member: %w", adaptererr.Decode(createErr))
	}

	return created.ID, true, nil
}

// AddToProject adds the member to the project idempotently (ON CONFLICT DO
// NOTHING). added is false when the membership already existed. Runs against the
// ambient transaction when the caller opened one.
func (s *MemberStore) AddToProject(ctx context.Context, membership repository.ProjectMembership) (added bool, err error) {
	domains := membership.Domains
	if domains == nil {
		domains = []string{}
	}

	rows, addErr := New(postgres.Conn(ctx, s.pool)).addProjectMemberIdempotent(ctx, addProjectMemberIdempotentParams{
		ProjectID: membership.ProjectID,
		MemberID:  membership.MemberID,
		Domains:   domains,
	})
	if addErr != nil {
		return false, fmt.Errorf("adding project member: %w", adaptererr.Decode(addErr))
	}

	return rows > 0, nil
}

// deliveryChannelOrDefault returns the channel as a string, substituting the
// dashboard default when the value is empty (e.g. a zero-value Member built
// before the field existed). The DB column is NOT NULL so an empty string would
// violate the CHECK constraint.
func deliveryChannelOrDefault(c model.DeliveryChannel) string {
	if c == "" {
		return string(model.DeliveryChannelDashboard)
	}

	return string(c)
}
