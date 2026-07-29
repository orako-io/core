// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/adapters/provider"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

// sweepInterval is how often the escalation rules are evaluated.
const sweepInterval = time.Minute

// escalationSweepStore is the read/write port the sweeper needs.
// *conversation.EscalationStore satisfies it. The default*Seconds parameters fill
// in for organizations without a stored value; a stored 0 disables the rule
// (resolved inside the queries).
type escalationSweepStore interface {
	UnclaimedForNudge(ctx context.Context, defaultSeconds int64) ([]model.Conversation, error)
	MarkNudged(ctx context.Context, conversationID uuid.UUID) (bool, error)
	UnclaimedForAlert(ctx context.Context, defaultSeconds int64) ([]model.AlertCandidate, error)
	MarkAlerted(ctx context.Context, conversationID uuid.UUID) (bool, error)
	UnclaimedForExpiry(ctx context.Context, defaultAlertSeconds int64) ([]model.Conversation, error)
	MarkExpired(ctx context.Context, conversationID uuid.UUID) (bool, error)
}

// alertProviderResolver resolves the messaging provider configured for a
// (project, kind) pair (ForProjectKind — the alert rung posts through the
// exact provider kind unclaimedForAlert resolved as owning the alert channel,
// never an ambiguous "the project's provider" when several kinds are
// configured) or for a specific recipient member (ForMember — the rung-2
// nudge fan-out routes per-candidate, mirroring the delivery notifier's and
// bridge projector's own use of the same seam). *provider.Registry satisfies
// both.
type alertProviderResolver interface {
	ForProjectKind(ctx context.Context, projectID uuid.UUID, kind provider.ProviderKind) (service.Provider, error)
	ForMember(ctx context.Context, projectID, memberID uuid.UUID) (service.Provider, error)
}

// candidatePoolAccess reads a conversation's candidates.
// *conversation.CandidateStore satisfies it.
type candidatePoolAccess interface {
	ByConversation(ctx context.Context, conversationID uuid.UUID) ([]model.Candidate, error)
}

// messageAppender persists a message. *conversation.Store satisfies it.
type messageAppender interface {
	AddMessage(ctx context.Context, msg model.Message) error
}

// eventPublisher appends to the durable log and fans out to subscribers.
// *messaging.GoChannelBus satisfies it.
type eventPublisher interface {
	Publish(ctx context.Context, env *orakov1.Envelope) (*orakov1.Envelope, error)
}

// EscalationSweeper is the background loop behind the pool's three escalation
// rules: nudge candidates of a conversation nobody answered (once), post to the
// project's alert channel when a pool question is still unanswered past a longer
// timeout (once), and finally mark a still-unanswered pool question timed_out
// once it passes the expiry timeout (the abandonment terminal state the
// dashboard "leads without an answer" KPI counts). Every action funnels through
// a compare-and-set, so a rerun or a concurrent sweep never double-fires.
type EscalationSweeper struct {
	store      escalationSweepStore
	candidates candidatePoolAccess
	messages   messageAppender
	bus        eventPublisher
	members    memberByIDReader
	providers  alertProviderResolver
	mailer     service.Mailer
	baseURL    string
	logger     *slog.Logger
}

// NewEscalationSweeper wires the sweeper. Dependencies must be non-nil.
func NewEscalationSweeper(
	store escalationSweepStore,
	candidates candidatePoolAccess,
	messages messageAppender,
	bus eventPublisher,
	members memberByIDReader,
	providers alertProviderResolver,
	mailer service.Mailer,
	baseURL string,
	logger *slog.Logger,
) *EscalationSweeper {
	return &EscalationSweeper{
		store:      store,
		candidates: candidates,
		messages:   messages,
		bus:        bus,
		members:    members,
		providers:  providers,
		mailer:     mailer,
		baseURL:    baseURL,
		logger:     logger,
	}
}

// Run evaluates the rules every sweepInterval until ctx is cancelled. It
// blocks, so callers run it in a dedicated goroutine next to the event router.
func (s *EscalationSweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

// sweep runs all three rules once, in escalating order. Failures are logged,
// never fatal: the next tick retries whatever a transient error skipped.
func (s *EscalationSweeper) sweep(ctx context.Context) {
	s.nudgeUnclaimed(ctx)
	s.alertUnclaimed(ctx)
	s.expireUnclaimed(ctx)
}

// expireUnclaimed marks pool conversations still open and unanswered past the
// expiry timeout as timed_out — the terminal "nobody ever answered" state. The
// MarkExpired compare-and-set flips status off 'open', so the transition (and
// its system note) fires exactly once per conversation even across concurrent
// sweeps, and never races a late answer. This is the only writer of
// status='timed_out'; without it the dashboard "leads without an answer" KPI is
// structurally always zero.
func (s *EscalationSweeper) expireUnclaimed(ctx context.Context) {
	convs, err := s.store.UnclaimedForExpiry(ctx, int64(model.DefaultAlertTimeout/time.Second))
	if err != nil {
		s.logger.WarnContext(ctx, "escalation: listing conversations for expiry", slog.Any("error", err))

		return
	}

	for _, conv := range convs {
		won, err := s.store.MarkExpired(ctx, conv.ID)
		if err != nil || !won {
			continue
		}

		if err := s.postSystemNote(ctx, conv, "No specialist answered in time — this question has timed out."); err != nil {
			s.logger.WarnContext(ctx, "escalation: posting timed-out note",
				slog.String("conversation_id", conv.ID.String()), slog.Any("error", err))
		}
	}
}

// nudgeUnclaimed reminds the candidates of pool conversations that sat
// unanswered past their org's nudge timeout. The MarkNudged compare-and-set
// makes the reminder fire exactly once per conversation.
func (s *EscalationSweeper) nudgeUnclaimed(ctx context.Context) {
	convs, err := s.store.UnclaimedForNudge(ctx, int64(model.DefaultClaimTimeout/time.Second))
	if err != nil {
		s.logger.WarnContext(ctx, "escalation: listing unanswered conversations", slog.Any("error", err))

		return
	}

	for _, conv := range convs {
		won, err := s.store.MarkNudged(ctx, conv.ID)
		if err != nil || !won {
			continue
		}

		if err := s.postSystemNote(ctx, conv, "Still waiting for an answer — the candidates have been reminded."); err != nil {
			s.logger.WarnContext(ctx, "escalation: posting nudge note",
				slog.String("conversation_id", conv.ID.String()), slog.Any("error", err))
		}

		s.nudgeActiveCandidates(ctx, conv)
	}
}

// nudgeActiveCandidates reminds every still-active candidate of a pool
// question unanswered past the nudge-timeout rung. A dashboard-bound candidate
// gets the existing email reminder (nudgeMember already filters on channel);
// an externally-bound candidate (Slack/Telegram/Teams/Discord) instead gets a
// provider nudge Deliver — fit#5: their t0 CONVERSATION_OPENED DM covered
// them then, but nothing today reaches them at this later rung, even though
// MessageKindNudge exists for exactly this. Per-row isolation: one failing
// candidate is logged and the fan-out continues; the caller's MarkNudged CAS
// already guarantees this whole fan-out runs at most once per conversation.
func (s *EscalationSweeper) nudgeActiveCandidates(ctx context.Context, conv model.Conversation) {
	pool, err := s.candidates.ByConversation(ctx, conv.ID)
	if err != nil {
		s.logger.WarnContext(ctx, "escalation: resolving candidates for nudge",
			slog.String("conversation_id", conv.ID.String()), slog.Any("error", err))

		return
	}

	// The email body only needs the ids and the question; reuse the opened
	// nudge template (mirrors emailActiveCandidates below).
	opened := &orakov1.ConversationOpened{
		ConversationId: conv.ID.String(),
		ProjectId:      conv.ProjectID.String(),
		Question:       conv.Question,
	}

	for _, candidate := range pool {
		if !candidate.Active() {
			continue
		}

		if err := nudgeMember(ctx, s.members, s.mailer, candidate.MemberID, opened, s.baseURL, s.logger); err != nil {
			s.logger.WarnContext(ctx, "escalation: emailing candidate nudge",
				slog.String("member_id", candidate.MemberID.String()), slog.Any("error", err))
		}

		s.deliverCandidateNudge(ctx, conv, candidate.MemberID)
	}
}

// deliverCandidateNudge sends a provider reminder to one candidate bound to a
// non-dashboard channel — the dashboard channel is already covered by the
// email reminder above and has no messaging provider to Deliver through. A
// resolution or delivery failure is logged and never stops the fan-out to the
// remaining candidates.
func (s *EscalationSweeper) deliverCandidateNudge(ctx context.Context, conv model.Conversation, memberID uuid.UUID) {
	member, err := s.members.ByID(ctx, memberID)
	if err != nil {
		s.logger.WarnContext(ctx, "escalation: resolving candidate for provider nudge",
			slog.String("member_id", memberID.String()), slog.Any("error", err))

		return
	}

	if member.DeliveryChannel == "" || member.DeliveryChannel == model.DeliveryChannelDashboard {
		return // dashboard channel: already covered by the email reminder above.
	}

	prov, err := s.providers.ForMember(ctx, conv.ProjectID, memberID)
	if err != nil {
		s.logger.WarnContext(ctx, "escalation: resolving provider for candidate nudge",
			slog.String("member_id", memberID.String()), slog.Any("error", err))

		return
	}

	if _, err := prov.Deliver(ctx, service.OutboundMessage{
		ProjectID:         conv.ProjectID,
		ConversationID:    conv.ID,
		ResponderMemberID: uuid.Nil,
		RecipientMemberID: memberID,
		Kind:              service.MessageKindNudge,
		Question:          "Still waiting for an answer — this question is still open.",
		Context:           "",
	}); err != nil {
		s.logger.WarnContext(ctx, "escalation: delivering candidate nudge",
			slog.String("member_id", memberID.String()), slog.Any("error", err))
	}
}

// alertUnclaimed is the channel-alert escalation rung: a pool question still
// unanswered past the org's (longer) alert timeout gets posted once to the
// project's alert channel, falling back to the org-wide default channel.
// Neither configured is a silent skip — there is nowhere to post, and the CAS
// is never spent, so a later configuration change lets a subsequent sweep
// pick the conversation back up. The CAS is won BEFORE the post (mirrors
// nudgeUnclaimed), so a concurrent sweep never double-posts; the accepted
// trade-off is the same as the nudge rule: a post that fails after a won CAS
// is not retried.
func (s *EscalationSweeper) alertUnclaimed(ctx context.Context) {
	convs, err := s.store.UnclaimedForAlert(ctx, int64(model.DefaultAlertTimeout/time.Second))
	if err != nil {
		s.logger.WarnContext(ctx, "escalation: listing unclaimed conversations for alert", slog.Any("error", err))

		return
	}

	for _, conv := range convs {
		channels := conv.AlertChannelIDs()
		if len(channels) == 0 {
			continue // no project or org channel configured: skip silently, CAS untouched.
		}

		won, err := s.store.MarkAlerted(ctx, conv.ConversationID)
		if err != nil || !won {
			continue
		}

		if conv.ProjectAlertKind == "" {
			s.logger.WarnContext(ctx, "escalation: no provider kind resolved for alert",
				slog.String("conversation_id", conv.ConversationID.String()))

			continue
		}

		prov, err := s.providers.ForProjectKind(ctx, conv.ProjectID, provider.ProviderKind(conv.ProjectAlertKind))
		if err != nil {
			s.logger.WarnContext(ctx, "escalation: resolving project provider for alert",
				slog.String("conversation_id", conv.ConversationID.String()),
				slog.String("kind", conv.ProjectAlertKind),
				slog.Any("error", err))

			continue
		}

		poster, ok := prov.(service.ChannelPoster)
		if !ok {
			s.logger.WarnContext(ctx, "escalation: project provider cannot post to a channel",
				slog.String("conversation_id", conv.ConversationID.String()))

			continue
		}

		// One MarkAlerted CAS was already won above, so the alert fires exactly
		// once per conversation; within that, post to every configured channel.
		text := alertText(conv, s.baseURL)
		for _, channelID := range channels {
			if _, err := poster.PostChannel(ctx, channelID, text); err != nil {
				s.logger.WarnContext(ctx, "escalation: posting alert",
					slog.String("conversation_id", conv.ConversationID.String()),
					slog.String("channel_id", channelID),
					slog.Any("error", err))
			}
		}
	}
}

// alertText builds the alert-channel post: the question title, how long it
// has waited, how many candidates were pinged, and the inbox link.
func alertText(conv model.AlertCandidate, baseURL string) string {
	inboxURL := strings.TrimRight(baseURL, "/") + "/inbox"
	waited := humanizeDuration(time.Since(conv.CreatedAt))

	return fmt.Sprintf("❓ Unanswered question: %q — waiting %s, %d candidate(s) notified. %s",
		conv.DisplayTitle(), waited, conv.CandidateCount, inboxURL)
}

// humanizeDuration renders d at minute granularity (e.g. "2h5m", "45m"),
// never finer — the alert is ambient signal, not a stopwatch.
func humanizeDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	if d <= 0 {
		return "under a minute"
	}

	hours := d / time.Hour
	minutes := (d % time.Hour) / time.Minute

	if hours == 0 {
		return fmt.Sprintf("%dm", minutes)
	}

	if minutes == 0 {
		return fmt.Sprintf("%dh", hours)
	}

	return fmt.Sprintf("%dh%dm", hours, minutes)
}

// postSystemNote appends an authorless system message to the thread and
// publishes its MESSAGE_POSTED so live dashboards see it.
func (s *EscalationSweeper) postSystemNote(ctx context.Context, conv model.Conversation, body string) error {
	msgID := uuid.New()

	msg, err := model.NewMessage(msgID, conv.ID, uuid.Nil, model.MessageRoleSystem, body, model.MessageSourceSystem)
	if err != nil {
		return err
	}

	if err := s.messages.AddMessage(ctx, msg); err != nil {
		return err
	}

	if _, err := s.bus.Publish(ctx, &orakov1.Envelope{
		ProjectId: conv.ProjectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_MESSAGE_POSTED,
		Payload: &orakov1.Envelope_MessagePosted{
			MessagePosted: &orakov1.MessagePosted{
				ConversationId: conv.ID.String(),
				MessageId:      msgID.String(),
				AuthorMemberId: "",
				Role:           orakov1.MessageRole_MESSAGE_ROLE_SYSTEM,
				Body:           body,
			},
		},
	}); err != nil {
		return fmt.Errorf("publishing message_posted(system): %w", err)
	}

	return nil
}
