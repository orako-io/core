// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/adapters/provider"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

// ---- sweeper fakes ----------------------------------------------------------

type fakeSweepStore struct {
	unclaimed   []model.Conversation
	alertReady  []model.AlertCandidate
	expiryReady []model.Conversation

	nudged  map[uuid.UUID]bool // pre-seeded true = already nudged (CAS loses)
	alerted map[uuid.UUID]bool // pre-seeded true = already alerted (CAS loses)
	expired map[uuid.UUID]bool // pre-seeded true = already timed_out (CAS loses)

	err error
}

func newFakeSweepStore() *fakeSweepStore {
	return &fakeSweepStore{
		nudged:  make(map[uuid.UUID]bool),
		alerted: make(map[uuid.UUID]bool),
		expired: make(map[uuid.UUID]bool),
	}
}

func (f *fakeSweepStore) UnclaimedForNudge(_ context.Context, _ int64) ([]model.Conversation, error) {
	return f.unclaimed, f.err
}

func (f *fakeSweepStore) MarkNudged(_ context.Context, conversationID uuid.UUID) (bool, error) {
	if f.nudged[conversationID] {
		return false, nil
	}

	f.nudged[conversationID] = true

	return true, nil
}

func (f *fakeSweepStore) UnclaimedForAlert(_ context.Context, _ int64) ([]model.AlertCandidate, error) {
	return f.alertReady, f.err
}

func (f *fakeSweepStore) MarkAlerted(_ context.Context, conversationID uuid.UUID) (bool, error) {
	if f.alerted[conversationID] {
		return false, nil
	}

	f.alerted[conversationID] = true

	return true, nil
}

func (f *fakeSweepStore) UnclaimedForExpiry(_ context.Context, _ int64) ([]model.Conversation, error) {
	return f.expiryReady, f.err
}

func (f *fakeSweepStore) MarkExpired(_ context.Context, conversationID uuid.UUID) (bool, error) {
	if f.expired[conversationID] {
		return false, nil
	}

	f.expired[conversationID] = true

	return true, nil
}

type fakeCandidateAccess struct {
	candidates map[uuid.UUID][]model.Candidate
}

func newFakeCandidateAccess() *fakeCandidateAccess {
	return &fakeCandidateAccess{
		candidates: make(map[uuid.UUID][]model.Candidate),
	}
}

func (f *fakeCandidateAccess) ByConversation(_ context.Context, conversationID uuid.UUID) ([]model.Candidate, error) {
	return f.candidates[conversationID], nil
}

type fakeMessageStore struct {
	messages []model.Message
	err      error
}

func (f *fakeMessageStore) AddMessage(_ context.Context, msg model.Message) error {
	if f.err != nil {
		return f.err
	}

	f.messages = append(f.messages, msg)

	return nil
}

type fakeBus struct {
	mu        sync.Mutex
	published []*orakov1.Envelope
}

func (f *fakeBus) Publish(_ context.Context, env *orakov1.Envelope) (*orakov1.Envelope, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, env)

	return env, nil
}

type sweeperHarness struct {
	store      *fakeSweepStore
	candidates *fakeCandidateAccess
	messages   *fakeMessageStore
	bus        *fakeBus
	providers  *fakeProjectProviders
	mailer     *fakeMailer
	sweeper    *EscalationSweeper
}

func newSweeperHarness() *sweeperHarness {
	h := &sweeperHarness{
		store:      newFakeSweepStore(),
		candidates: newFakeCandidateAccess(),
		messages:   &fakeMessageStore{},
		bus:        &fakeBus{},
		providers: &fakeProjectProviders{
			byProjectKind: make(map[fakeProjectProviderKey]service.Provider),
			byMember:      make(map[uuid.UUID]service.Provider),
		},
		mailer: &fakeMailer{},
	}

	members := &fakeMemberReader{member: model.Member{
		DisplayName:     "Sam",
		Email:           "sam@example.com",
		DeliveryChannel: model.DeliveryChannelDashboard,
	}}

	h.sweeper = NewEscalationSweeper(h.store, h.candidates, h.messages, h.bus,
		members, h.providers, h.mailer, "https://orako.example.com", quietLogger())

	return h
}

// ---- nudge -------------------------------------------------------------------

// TestEscalation_NudgeFiresExactlyOnce proves the once-only marker: two sweeps
// over the same unclaimed conversation produce one note and one email round.
func TestEscalation_NudgeFiresExactlyOnce(t *testing.T) {
	t.Parallel()

	h := newSweeperHarness()

	conv := model.Conversation{
		ID:            uuid.New(),
		ProjectID:     uuid.New(),
		AskerMemberID: uuid.New(),
		Status:        model.ConversationStatusOpen,
		Question:      "anyone?",
	}
	h.store.unclaimed = []model.Conversation{conv}
	h.candidates.candidates[conv.ID] = []model.Candidate{{MemberID: uuid.New()}, {MemberID: uuid.New()}}

	h.sweeper.sweep(t.Context())
	h.sweeper.sweep(t.Context())

	if got := len(h.messages.messages); got != 1 {
		t.Fatalf("want 1 nudge note after two sweeps, got %d", got)
	}

	if got := h.messages.messages[0].Body; got != "Still waiting for an answer — the candidates have been reminded." {
		t.Errorf("nudge note body = %q", got)
	}

	if len(h.mailer.sent) != 2 {
		t.Errorf("want 2 reminder emails (one per candidate, once), got %d", len(h.mailer.sent))
	}
}

// TestEscalation_NudgeReachesExternalBoundCandidates proves fit#5: at the
// rung-2 (claim-timeout) nudge, a Slack-bound candidate gets a provider nudge
// Deliver (Kind=nudge) in addition to a dashboard-bound candidate's existing
// email reminder — and a second sweep over the same conversation fires
// neither again (the nudged_at CAS guard).
func TestEscalation_NudgeReachesExternalBoundCandidates(t *testing.T) {
	t.Parallel()

	store := newFakeSweepStore()
	candidates := newFakeCandidateAccess()
	messages := &fakeMessageStore{}
	bus := &fakeBus{}
	mailer := &fakeMailer{}

	slackCandidateID := uuid.New()
	dashboardCandidateID := uuid.New()

	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: slackCandidateID, DisplayName: "Slacky", DeliveryChannel: model.DeliveryChannelSlack, SlackUserID: "USLACKY"})
	members.add(model.Member{ID: dashboardCandidateID, DisplayName: "Dash", Email: "dash@example.com", DeliveryChannel: model.DeliveryChannelDashboard})

	slackProv := &fakeEditorProvider{}
	providers := &fakeProjectProviders{
		byProjectKind: make(map[fakeProjectProviderKey]service.Provider),
		byMember:      map[uuid.UUID]service.Provider{slackCandidateID: slackProv},
	}

	sweeper := NewEscalationSweeper(store, candidates, messages, bus, members, providers, mailer,
		"https://orako.example.com", quietLogger())

	conv := model.Conversation{
		ID:            uuid.New(),
		ProjectID:     uuid.New(),
		AskerMemberID: uuid.New(),
		Status:        model.ConversationStatusOpen,
		Question:      "Which index fits?",
	}
	store.unclaimed = []model.Conversation{conv}
	candidates.candidates[conv.ID] = []model.Candidate{{MemberID: slackCandidateID}, {MemberID: dashboardCandidateID}}

	sweeper.sweep(t.Context())

	if len(slackProv.delivered) != 1 {
		t.Fatalf("want 1 provider nudge Deliver to the Slack-bound candidate, got %d", len(slackProv.delivered))
	}

	if got := slackProv.delivered[0].Kind; got != service.MessageKindNudge {
		t.Errorf("delivered Kind = %q, want nudge", got)
	}

	if got := slackProv.delivered[0].RecipientMemberID; got != slackCandidateID {
		t.Errorf("delivered recipient = %v, want the Slack-bound candidate %v", got, slackCandidateID)
	}

	if len(mailer.sent) != 1 {
		t.Fatalf("want 1 email reminder to the dashboard-bound candidate, got %d", len(mailer.sent))
	}

	if mailer.sent[0].To != "dash@example.com" {
		t.Errorf("email recipient = %q, want dash@example.com", mailer.sent[0].To)
	}

	// A second sweep must not re-fire either channel: the nudged_at CAS
	// already spent itself on the first sweep.
	sweeper.sweep(t.Context())

	if len(slackProv.delivered) != 1 {
		t.Errorf("want still 1 provider nudge after a second sweep, got %d", len(slackProv.delivered))
	}

	if len(mailer.sent) != 1 {
		t.Errorf("want still 1 email reminder after a second sweep, got %d", len(mailer.sent))
	}
}

// TestEscalation_SweepSurvivesStoreErrors proves a listing failure only skips
// the tick (logged), never panics or fires actions.
func TestEscalation_SweepSurvivesStoreErrors(t *testing.T) {
	t.Parallel()

	h := newSweeperHarness()
	h.store.err = errors.New("db down")

	h.sweeper.sweep(t.Context())

	if len(h.messages.messages) != 0 || len(h.mailer.sent) != 0 {
		t.Error("a failing store must not trigger actions")
	}
}

// ---- alert-channel rung (task 4, phase 3) ----------------------------------

// TestEscalation_AlertPostsOnceToProjectChannel proves the third rung posts
// exactly once to the project's own alert channel and sets alerted_at (the
// fake store's CAS).
func TestEscalation_AlertPostsOnceToProjectChannel(t *testing.T) {
	t.Parallel()

	h := newSweeperHarness()

	projectID := uuid.New()
	convID := uuid.New()

	h.store.alertReady = []model.AlertCandidate{{
		ConversationID:         convID,
		ProjectID:              projectID,
		Question:               "Which index fits?",
		CreatedAt:              time.Now().Add(-3 * time.Hour),
		ProjectAlertChannelIDs: []string{"PROJCHAN"},
		ProjectAlertKind:       "slack",
		OrgAlertChannelID:      "ORGCHAN",
		CandidateCount:         2,
	}}

	poster := &fakeChannelPosterProvider{}
	h.providers.byProjectKind[fakeProjectProviderKey{projectID: projectID, kind: provider.ProviderKindSlack}] = poster

	h.sweeper.sweep(t.Context())

	if len(poster.posted) != 1 {
		t.Fatalf("want 1 channel post, got %d", len(poster.posted))
	}

	if poster.posted[0].channelID != "PROJCHAN" {
		t.Errorf("posted channel = %q, want the project channel PROJCHAN", poster.posted[0].channelID)
	}

	if !strings.HasPrefix(poster.posted[0].text, "❓ Unanswered question:") {
		t.Errorf("alert text = %q, want the unanswered-question prefix", poster.posted[0].text)
	}

	if !h.store.alerted[convID] {
		t.Error("want alerted_at set after a successful post")
	}

	// A second sweep must not post again: the fake CAS already fired.
	h.sweeper.sweep(t.Context())

	if len(poster.posted) != 1 {
		t.Errorf("want still 1 channel post after a second sweep, got %d", len(poster.posted))
	}
}

// TestEscalation_AlertFallsBackToOrgChannel proves an empty project channel
// falls back to the org default.
func TestEscalation_AlertFallsBackToOrgChannel(t *testing.T) {
	t.Parallel()

	h := newSweeperHarness()

	projectID := uuid.New()
	convID := uuid.New()

	h.store.alertReady = []model.AlertCandidate{{
		ConversationID:    convID,
		ProjectID:         projectID,
		Question:          "Which index fits?",
		CreatedAt:         time.Now().Add(-3 * time.Hour),
		ProjectAlertKind:  "slack",
		OrgAlertChannelID: "ORGCHAN",
		CandidateCount:    1,
	}}

	poster := &fakeChannelPosterProvider{}
	h.providers.byProjectKind[fakeProjectProviderKey{projectID: projectID, kind: provider.ProviderKindSlack}] = poster

	h.sweeper.sweep(t.Context())

	if len(poster.posted) != 1 || poster.posted[0].channelID != "ORGCHAN" {
		t.Fatalf("posted = %+v, want exactly one post to ORGCHAN", poster.posted)
	}
}

// TestEscalation_AlertPostsThroughOwningKindOnMultiProviderProject is the
// review fit#4 scenario at the alert rung: a project has both Slack and
// Discord configured, but the alert channel belongs to Discord
// (unclaimedForAlert resolved ProjectAlertKind="discord"). The sweeper must
// post through Discord and must never touch the Slack provider, even though
// Slack is also registered for this project.
func TestEscalation_AlertPostsThroughOwningKindOnMultiProviderProject(t *testing.T) {
	t.Parallel()

	h := newSweeperHarness()

	projectID := uuid.New()
	convID := uuid.New()

	h.store.alertReady = []model.AlertCandidate{{
		ConversationID:         convID,
		ProjectID:              projectID,
		Question:               "Which index fits?",
		CreatedAt:              time.Now().Add(-3 * time.Hour),
		ProjectAlertChannelIDs: []string{"DISCORDCHAN"},
		ProjectAlertKind:       "discord",
		OrgAlertChannelID:      "ORGCHAN",
		CandidateCount:         1,
	}}

	slackPoster := &fakeChannelPosterProvider{}
	discordPoster := &fakeChannelPosterProvider{}
	h.providers.byProjectKind[fakeProjectProviderKey{projectID: projectID, kind: provider.ProviderKindSlack}] = slackPoster
	h.providers.byProjectKind[fakeProjectProviderKey{projectID: projectID, kind: provider.ProviderKindDiscord}] = discordPoster

	h.sweeper.sweep(t.Context())

	if len(discordPoster.posted) != 1 || discordPoster.posted[0].channelID != "DISCORDCHAN" {
		t.Fatalf("discord posted = %+v, want exactly one post to DISCORDCHAN", discordPoster.posted)
	}

	if len(slackPoster.posted) != 0 {
		t.Errorf("slack provider must not be posted to: got %+v", slackPoster.posted)
	}
}

// TestEscalation_AlertSkipsSilentlyWhenNoChannelConfigured proves neither
// channel configured is a no-op: no post, no CAS spent (so a later config
// change can still alert).
func TestEscalation_AlertSkipsSilentlyWhenNoChannelConfigured(t *testing.T) {
	t.Parallel()

	h := newSweeperHarness()

	projectID := uuid.New()
	convID := uuid.New()

	h.store.alertReady = []model.AlertCandidate{{
		ConversationID: convID,
		ProjectID:      projectID,
		Question:       "Which index fits?",
		CreatedAt:      time.Now().Add(-3 * time.Hour),
		CandidateCount: 1,
	}}

	// No provider registered at all: if the sweeper tried to post it would
	// fail resolving the provider, proving the skip happens before that.
	h.sweeper.sweep(t.Context())

	if h.store.alerted[convID] {
		t.Error("want alerted_at untouched when no channel is configured")
	}
}

// ---- expiry rung (timed_out transition) ------------------------------------

// TestEscalation_ExpiryTransitionsOnce proves the third rung marks a still-open,
// still-unanswered pool conversation timed_out exactly once: two sweeps over the
// same conversation flip it once (the MarkExpired CAS) and post one system note.
func TestEscalation_ExpiryTransitionsOnce(t *testing.T) {
	t.Parallel()

	h := newSweeperHarness()

	conv := model.Conversation{
		ID:            uuid.New(),
		ProjectID:     uuid.New(),
		AskerMemberID: uuid.New(),
		Status:        model.ConversationStatusOpen,
		Question:      "did anyone ever look at this?",
	}
	h.store.expiryReady = []model.Conversation{conv}

	h.sweeper.sweep(t.Context())
	h.sweeper.sweep(t.Context())

	if !h.store.expired[conv.ID] {
		t.Fatal("want the conversation marked timed_out after the sweep")
	}

	if got := len(h.messages.messages); got != 1 {
		t.Fatalf("want exactly 1 timed-out note after two sweeps, got %d", got)
	}

	if got := h.messages.messages[0].Body; got != "No specialist answered in time — this question has timed out." {
		t.Errorf("timed-out note body = %q", got)
	}
}

// TestEscalation_ExpirySkipsWhenCASLost proves that when the transition was
// already applied (a concurrent sweep, or a late answer moved the status), the
// rung posts no note.
func TestEscalation_ExpirySkipsWhenCASLost(t *testing.T) {
	t.Parallel()

	h := newSweeperHarness()

	conv := model.Conversation{
		ID:            uuid.New(),
		ProjectID:     uuid.New(),
		AskerMemberID: uuid.New(),
		Status:        model.ConversationStatusOpen,
		Question:      "stale?",
	}
	h.store.expiryReady = []model.Conversation{conv}
	h.store.expired[conv.ID] = true // CAS already lost (someone else expired it)

	h.sweeper.sweep(t.Context())

	if len(h.messages.messages) != 0 {
		t.Errorf("want no note when the expiry CAS is lost, got %d", len(h.messages.messages))
	}
}

// TestEscalation_RunStopsWithContext proves the goroutine exits with the
// server ctx (no leak).
func TestEscalation_RunStopsWithContext(t *testing.T) {
	t.Parallel()

	h := newSweeperHarness()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		h.sweeper.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sweeper did not stop with the context")
	}
}
