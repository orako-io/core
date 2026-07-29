// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/errs"
)

// Sentinel errors surfaced when a join code cannot be redeemed. They carry no
// org/project detail — a stranger probing codes learns only "invalid" or
// "revoked", never which org a guessed code belongs to.
var (
	// ErrJoinTokenInvalid is returned when the presented code matches no token.
	ErrJoinTokenInvalid = errors.New("join code is not valid")
	// ErrJoinTokenRevoked is returned when the code exists but has been revoked.
	ErrJoinTokenRevoked = errors.New("join code has been revoked")
)

// RedeemJoinTokenCommand provisions the authenticated account behind Token as a
// pending member of the token's org/project. AccountID and Token are the trust
// inputs; Email/DisplayName are cosmetic identity carried from the account.
type RedeemJoinTokenCommand struct {
	AccountID   uuid.UUID
	Token       string
	Email       string `exhaustruct:"optional"`
	DisplayName string `exhaustruct:"optional"`
}

// RedeemJoinTokenResult carries the provisioned member and the project the code
// scopes to — the OAuth consent seam mints the auth code against this project.
type RedeemJoinTokenResult struct {
	MemberID  uuid.UUID
	ProjectID uuid.UUID
	// AlreadyMember is true when the account already had a member (idempotent
	// redemption): nothing was created and no existing status was changed.
	AlreadyMember bool `exhaustruct:"optional"`
}

// joinTokenReader resolves a raw code to its landing org/project. The org and
// project come ONLY from this row. *identity.JoinTokenStore satisfies it.
type joinTokenReader interface {
	JoinTokenByToken(ctx context.Context, token string) (repository.JoinToken, error)
}

// pendingMemberProvisioner creates an account-linked pending member and adds it
// to the code's project. *identity.MemberStore satisfies it.
type pendingMemberProvisioner interface {
	CreateWithAccount(ctx context.Context, member model.Member) error
	AddToProject(ctx context.Context, membership repository.ProjectMembership) (added bool, err error)
}

// RedeemJoinTokenHandler handles RedeemJoinTokenCommand.
type RedeemJoinTokenHandler struct {
	tokens           joinTokenReader
	members          pendingMemberProvisioner
	membersByAccount memberByAccountReader
	txor             service.Transactor
	gate             SeatGate `exhaustruct:"optional"`
}

// memberByAccountReader resolves the account's primary member WITHIN one org —
// used only for the idempotency guard (is this account already a member of the
// CODE'S org?). Scoped to the org so an account that is a live member of another
// org is not wrongly treated as already-a-member here.
type memberByAccountReader interface {
	ByAccountInOrg(ctx context.Context, accountID, orgID uuid.UUID) (model.Member, error)
}

// MustNewRedeemJoinTokenHandler builds the handler. It panics on nil
// required deps. gate may be nil, which disables seat enforcement (community /
// self-host); when set (SaaS) a join respects the org's seat cap.
func MustNewRedeemJoinTokenHandler(
	tokens joinTokenReader,
	members pendingMemberProvisioner,
	membersByAccount memberByAccountReader,
	txor service.Transactor,
	gate SeatGate,
) RedeemJoinTokenHandler {
	if tokens == nil || members == nil || membersByAccount == nil {
		panic("RedeemJoinTokenHandler requires non-nil token reader and member stores")
	}

	if txor == nil {
		panic("RedeemJoinTokenHandler requires a non-nil service.Transactor")
	}

	return RedeemJoinTokenHandler{tokens: tokens, members: members, membersByAccount: membersByAccount, txor: txor, gate: gate}
}

// Handle redeems the code and provisions the account as a pending member.
//
// SECURITY: the org and project a redemption lands in are read ONLY from the
// token row — never from any caller-supplied field — so a code can only ever
// admit its bearer to the org/project it was minted for. A revoked or unknown
// code is rejected with a detail-free sentinel.
func (h RedeemJoinTokenHandler) Handle(ctx context.Context, cmd RedeemJoinTokenCommand) (RedeemJoinTokenResult, error) {
	if cmd.AccountID == uuid.Nil {
		return RedeemJoinTokenResult{}, errs.InvalidError{Field: "account_id", Reason: reasonNilUUID}
	}

	token := strings.TrimSpace(cmd.Token)
	if token == "" {
		return RedeemJoinTokenResult{}, errs.InvalidError{Field: "join_token", Reason: "must not be empty"}
	}

	jt, err := h.tokens.JoinTokenByToken(ctx, token)
	if err != nil {
		if errors.Is(err, adaptererr.ErrNotFound) {
			return RedeemJoinTokenResult{}, ErrJoinTokenInvalid
		}

		return RedeemJoinTokenResult{}, translateErr(err, "join_token")
	}

	if jt.Revoked {
		return RedeemJoinTokenResult{}, ErrJoinTokenRevoked
	}

	// Org + project come ONLY from the token row.
	orgID, projectID := jt.OrgID, jt.ProjectID

	// Idempotency: if the account already owns a LIVE member IN THIS CODE'S ORG,
	// return it unchanged — never duplicate, and never downgrade an existing active
	// member to pending. The check is ORG-SCOPED (jt.OrgID): an account that is a
	// live member of a DIFFERENT org must still be able to redeem this code and get
	// a fresh pending member in this org. A DEAD member (removed/purged/deactivated/
	// suspended) is NOT idempotency: the person was offboarded and is re-joining via
	// a fresh code, so we fall through and provision a brand-new pending member.
	// ByAccountInOrg prefers a live member, so an account holding both a dead and a
	// live member in this org is treated as already a member here.
	if existing, lookupErr := h.membersByAccount.ByAccountInOrg(ctx, cmd.AccountID, orgID); lookupErr == nil {
		if existing.Status.CanAuthenticate() {
			return RedeemJoinTokenResult{MemberID: existing.ID, ProjectID: projectID, AlreadyMember: true}, nil
		}
	} else if !errors.Is(lookupErr, adaptererr.ErrNotFound) {
		return RedeemJoinTokenResult{}, translateErr(lookupErr, "member")
	}

	// Seat gate (SaaS only): a join respects the org's seat cap / trial state.
	if h.gate != nil {
		if gateErr := h.gate.AllowNewMember(ctx, projectID); gateErr != nil {
			return RedeemJoinTokenResult{}, gateErr
		}
	}

	memberID := uuid.New()

	member, err := model.NewPendingMember(memberID, cmd.AccountID, cmd.Email, displayNameOrEmail(cmd.DisplayName, cmd.Email))
	if err != nil {
		return RedeemJoinTokenResult{}, err
	}

	if err := h.provision(ctx, member, projectID); err != nil {
		return RedeemJoinTokenResult{}, translateErr(err, "member")
	}

	slog.InfoContext(ctx, "join code redeemed: account provisioned as pending member",
		slog.String("account_id", cmd.AccountID.String()),
		slog.String("member_id", memberID.String()),
		slog.String("org_id", orgID.String()),
		slog.String("project_id", projectID.String()))

	return RedeemJoinTokenResult{MemberID: memberID, ProjectID: projectID}, nil
}

// provision creates the pending member and adds it to the code's project in one
// transaction, mirroring the org-creation provisioning shape. Regular members'
// org scope is implicit via project_members → projects.org_id, so no org_members
// row is written (matching the invite path).
func (h RedeemJoinTokenHandler) provision(ctx context.Context, member model.Member, projectID uuid.UUID) error {
	return h.txor.WithTx(ctx, func(ctx context.Context) error {
		if err := h.members.CreateWithAccount(ctx, member); err != nil {
			return err
		}

		if _, err := h.members.AddToProject(ctx, repository.ProjectMembership{
			ProjectID: projectID,
			MemberID:  member.ID,
			Role:      model.RoleUnspecified,
			Domains:   nil,
		}); err != nil {
			return err
		}

		return nil
	})
}

// displayNameOrEmail falls back to the email when no display name is known, so a
// pending member is never nameless in the roster.
func displayNameOrEmail(displayName, email string) string {
	if strings.TrimSpace(displayName) != "" {
		return displayName
	}

	return email
}
