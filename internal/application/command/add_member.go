// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/errs"
)

// AddMemberCommand is the input for adding a member to a project. If the
// member does not yet exist in Orako it is created first.
type AddMemberCommand struct {
	ProjectID uuid.UUID
	// Email is the member's canonical email used as the match key.
	Email       string
	DisplayName string
	// SlackUserID and TelegramChatID are optional channel bindings; empty is
	// allowed when the project uses a different provider.
	SlackUserID    string `exhaustruct:"optional"`
	TelegramChatID string `exhaustruct:"optional"`
	// Domains are the expertise tags applied to the new membership.
	Domains []string `exhaustruct:"optional"`
	// Resend explicitly re-sends the invitation email to a still-pending member
	// (the dashboard's "Resend" button). Without it, re-adding an existing
	// member — e.g. the invite modal looping over several selected projects —
	// stays silent, so a new member gets exactly ONE invitation email, never one
	// per project.
	Resend bool `exhaustruct:"optional"`
}

// AddMemberResult carries the member ID (new or existing) and whether the
// member was already on the project (no fresh invite sent) — the bulk-invite
// summary reads this to report invited-vs-already per email.
type AddMemberResult struct {
	MemberID      uuid.UUID
	AlreadyMember bool `exhaustruct:"optional"`
}

// SeatGate authorizes adding a member to a project against the org's billing
// (seat limit / lapsed trial). A nil gate means enforcement is off — the
// self-host default, where billing is disabled entirely.
type SeatGate interface {
	// AllowNewMember returns a typed error when the project's org may not take
	// on another member, or nil to allow.
	AllowNewMember(ctx context.Context, projectID uuid.UUID) error
}

// AddMemberHandler handles AddMemberCommand.
type AddMemberHandler struct {
	provisioner memberProvisioner
	txor        service.Transactor
	members     memberChannelStore
	bus         eventBus
	gate        SeatGate              `exhaustruct:"optional"`
	directory   chatDirectoryResolver `exhaustruct:"optional"`
}

// MustNewAddMemberHandler builds a handler. It panics on nil
// dependencies. gate may be nil, which disables seat enforcement (self-host);
// directory may be nil, which disables Slack auto-bind by email.
func MustNewAddMemberHandler(
	provisioner memberProvisioner,
	txor service.Transactor,
	members memberChannelStore,
	bus eventBus,
	gate SeatGate,
	directory chatDirectoryResolver,
) AddMemberHandler {
	if provisioner == nil || members == nil {
		panic("AddMemberHandler requires non-nil memberProvisioner and member store")
	}

	if txor == nil {
		panic("AddMemberHandler requires a non-nil service.Transactor")
	}

	if bus == nil {
		panic("AddMemberHandler requires a non-nil eventBus")
	}

	return AddMemberHandler{provisioner: provisioner, txor: txor, members: members, bus: bus, gate: gate, directory: directory}
}

// autoBindSlack resolves the member's Slack id by email from the org's Slack
// workspace so the admin never types an id. Best-effort: any miss (no
// directory, user not in workspace, transient error) returns "" — the member
// stays on the email fallback, and the invite never fails.
func (h AddMemberHandler) autoBindSlack(ctx context.Context, projectID uuid.UUID, email string) string {
	if h.directory == nil {
		return ""
	}

	slackID, err := h.directory.LookupSlackByEmail(ctx, projectID, email)
	if err != nil {
		slog.DebugContext(ctx, "add_member: slack auto-bind skipped",
			slog.String("email", email), slog.Any("error", err))

		return ""
	}

	return slackID
}

// provision finds-or-creates the member and adds them to the project in one
// transaction, composed here rather than in a MemberAtomicStore adapter — each
// store call joins the ambient tx through its context. freshInvite is true for a
// newly created/reactivated member; alreadyMember is true when the membership
// already existed.
func (h AddMemberHandler) provision(ctx context.Context, email string, candidate model.Member, membership repository.ProjectMembership) (memberID uuid.UUID, freshInvite, alreadyMember bool, err error) {
	err = h.txor.WithTx(ctx, func(ctx context.Context) error {
		id, fresh, ferr := h.provisioner.FindOrCreateByEmail(ctx, email, candidate)
		if ferr != nil {
			return ferr
		}

		memberID, freshInvite = id, fresh
		membership.MemberID = id

		added, aerr := h.provisioner.AddToProject(ctx, membership)
		if aerr != nil {
			return aerr
		}

		alreadyMember = !added && !fresh

		return nil
	})

	return memberID, freshInvite, alreadyMember, err
}

// Handle creates or resolves the member, adds them to the project, auto-binds
// their Slack id by email when possible, and emits the right lifecycle event
// (exactly one invitation email per member).
func (h AddMemberHandler) Handle(ctx context.Context, cmd AddMemberCommand) (AddMemberResult, error) {
	// Seat gate (SaaS only): block growth when the org is at its paid seat cap
	// or its trial has lapsed. Skipped entirely when no gate is wired
	// (self-host). Existing members and the ask/answer flow are never affected —
	// this touches only the add path.
	if h.gate != nil {
		if err := h.gate.AllowNewMember(ctx, cmd.ProjectID); err != nil {
			return AddMemberResult{}, err
		}
	}

	// Build the candidate first so domain invariants are checked before any DB work.
	newMemberID := uuid.New()

	candidate, err := model.NewMember(newMemberID, cmd.Email, cmd.DisplayName)
	if err != nil {
		return AddMemberResult{}, err
	}

	candidate.SlackUserID = cmd.SlackUserID
	candidate.TelegramChatID = cmd.TelegramChatID

	if candidate.SlackUserID == "" {
		candidate.SlackUserID = h.autoBindSlack(ctx, cmd.ProjectID, cmd.Email)
	}

	membership := repository.ProjectMembership{
		ProjectID: cmd.ProjectID,
		MemberID:  uuid.Nil, // filled in once the member ID is resolved
		Role:      model.RoleUnspecified,
		Domains:   cmd.Domains,
	}

	memberID, freshInvite, alreadyMember, err := h.provision(ctx, cmd.Email, candidate, membership)
	if err != nil {
		return AddMemberResult{}, translateErr(err, "member")
	}

	// Exactly ONE invitation email per member, never one per project:
	//   - freshInvite (newly created / reactivated) → INVITED: the one email.
	//   - explicit Resend of a still-pending invitation → INVITED again.
	//   - existing PENDING member added to another project → silent (their
	//     org-level invitation already covers it; this used to email per project).
	//   - existing ACTIVE member added to a NEW project → ADDED_TO_PROJECT: the
	//     "you've been added" notification (coalesced downstream, so a
	//     multi-project add still yields a single email).
	//   - same membership re-added → silent (idempotent no-op).
	var transition orakov1.MemberTransition

	switch {
	case freshInvite:
		transition = orakov1.MemberTransition_MEMBER_TRANSITION_INVITED
	case cmd.Resend && stillInvited(ctx, h.members, memberID):
		transition = orakov1.MemberTransition_MEMBER_TRANSITION_INVITED
	case !alreadyMember && isActiveMember(ctx, h.members, memberID):
		transition = orakov1.MemberTransition_MEMBER_TRANSITION_ADDED_TO_PROJECT
	default:
		return AddMemberResult{MemberID: memberID, AlreadyMember: alreadyMember}, nil
	}

	if _, err = h.bus.Publish(ctx, &orakov1.Envelope{
		ProjectId: cmd.ProjectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_MEMBER_LIFECYCLE,
		Payload: &orakov1.Envelope_MemberLifecycle{
			MemberLifecycle: &orakov1.MemberLifecycle{
				MemberId:   memberID.String(),
				ProjectId:  cmd.ProjectID.String(),
				Transition: transition,
			},
		},
	}); err != nil {
		return AddMemberResult{}, errs.InternalError{Err: fmt.Errorf("publishing member_lifecycle: %w", err)}
	}

	return AddMemberResult{MemberID: memberID, AlreadyMember: alreadyMember}, nil
}

// isActiveMember reports whether the member is fully active (accepted their
// invitation). Read errors count as not-active so a failed lookup stays silent
// rather than emailing on a guess.
func isActiveMember(ctx context.Context, members memberChannelStore, id uuid.UUID) bool {
	m, err := members.ByID(ctx, id)

	return err == nil && m.Status == model.MemberStatusActive
}

// stillInvited reports whether the member's invitation is still pending. Read
// errors count as not-invited so idempotent re-adds stay silent.
func stillInvited(ctx context.Context, members memberChannelStore, id uuid.UUID) bool {
	m, err := members.ByID(ctx, id)

	return err == nil && m.Status == model.MemberStatusInvited
}
