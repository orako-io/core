// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/provider/slack"
	"github.com/orako-io/core/internal/adapters/provider/telegram"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

// ── shared fakes ─────────────────────────────────────────────────────────────

type fakeCandidatesReader struct {
	candidates []model.Candidate
	err        error
}

func (f *fakeCandidatesReader) ByConversation(_ context.Context, _ uuid.UUID) ([]model.Candidate, error) {
	return f.candidates, f.err
}

// fakeMemberBindingWriter is an in-memory memberBindingWriter: ByID reads the
// seeded map, Update overwrites it and records every call for assertions.
// failByID lets a test force a specific member's ByID call to fail, as if a
// transient DB blip hit that one candidate's lookup.
type fakeMemberBindingWriter struct {
	byID    map[uuid.UUID]model.Member
	byIDErr map[uuid.UUID]error
	updated []model.Member
}

func newFakeMemberBindingWriter() *fakeMemberBindingWriter {
	return &fakeMemberBindingWriter{byID: make(map[uuid.UUID]model.Member)}
}

func (f *fakeMemberBindingWriter) add(m model.Member) { f.byID[m.ID] = m }

func (f *fakeMemberBindingWriter) failByID(id uuid.UUID, err error) {
	if f.byIDErr == nil {
		f.byIDErr = make(map[uuid.UUID]error)
	}

	f.byIDErr[id] = err
}

func (f *fakeMemberBindingWriter) ByID(_ context.Context, id uuid.UUID) (model.Member, error) {
	if err, ok := f.byIDErr[id]; ok {
		return model.Member{}, err
	}

	m, ok := f.byID[id]
	if !ok {
		return model.Member{}, adaptererr.ErrNotFound
	}

	return m, nil
}

func (f *fakeMemberBindingWriter) Update(_ context.Context, m model.Member) error {
	f.byID[m.ID] = m
	f.updated = append(f.updated, m)

	return nil
}

// fakeLedgerWriter is an in-memory providerMessageLedgerWriter: Upsert
// records rows (or fails every call, if err is set) and ByConversation lists
// them back — the same rows a real replay would see, which is what makes it
// suitable for the duplicate-OPEN idempotency tests (call the handler twice
// against the same fakeLedgerWriter instance). SetState/Finalize mutate the
// matching row in place by id (or fail every call, if setStateErr/
// finalizeErr is set), mirroring the reserve→deliver→finalize sequence
// deliverToCandidate drives.
type fakeLedgerWriter struct {
	rows      []model.ProviderMessage
	err       error
	byConvErr error

	setStateErr error
	finalizeErr error

	finalizeCalls int
}

func (f *fakeLedgerWriter) Upsert(_ context.Context, msg model.ProviderMessage) error {
	if f.err != nil {
		return f.err
	}

	f.rows = append(f.rows, msg)

	return nil
}

func (f *fakeLedgerWriter) ByConversation(_ context.Context, _ uuid.UUID) ([]model.ProviderMessage, error) {
	if f.byConvErr != nil {
		return nil, f.byConvErr
	}

	return f.rows, nil
}

func (f *fakeLedgerWriter) SetState(_ context.Context, id uuid.UUID, state model.ProviderMessageState) error {
	if f.setStateErr != nil {
		return f.setStateErr
	}

	for i, r := range f.rows {
		if r.ID == id {
			f.rows[i].State = state
		}
	}

	return nil
}

func (f *fakeLedgerWriter) Finalize(_ context.Context, id uuid.UUID, channelID, messageRef string, state model.ProviderMessageState) error {
	f.finalizeCalls++

	if f.finalizeErr != nil {
		return f.finalizeErr
	}

	for i, r := range f.rows {
		if r.ID == id {
			f.rows[i].ChannelID = channelID
			f.rows[i].MessageRef = messageRef
			f.rows[i].State = state
		}
	}

	return nil
}

// fakeCandidateProviders resolves a real Provider per member id — a more
// granular seam than the production Registry (one provider per project)
// affords today, but the right shape to unit-test the notifier's own
// per-candidate fan-out/fallback logic in isolation. failByMember lets a test
// force ForMember to fail for one member with an arbitrary error (transient
// resolver failure or service.ErrNoProvider).
type fakeCandidateProviders struct {
	byMember    map[uuid.UUID]service.Provider
	byMemberErr map[uuid.UUID]error
}

func (f *fakeCandidateProviders) failByMember(id uuid.UUID, err error) {
	if f.byMemberErr == nil {
		f.byMemberErr = make(map[uuid.UUID]error)
	}

	f.byMemberErr[id] = err
}

func (f *fakeCandidateProviders) ForMember(_ context.Context, _, memberID uuid.UUID) (service.Provider, error) {
	if err, ok := f.byMemberErr[memberID]; ok {
		return nil, err
	}

	p, ok := f.byMember[memberID]
	if !ok {
		return nil, errors.New("fakeCandidateProviders: no provider configured for member")
	}

	return p, nil
}

// poolOpenedMsg builds a CONVERSATION_OPENED envelope for a pool dispatch
// (empty responder id) with explicit, assertable project/conversation ids.
func poolOpenedMsg(t *testing.T, projectID, conversationID uuid.UUID, question string) *message.Message {
	t.Helper()

	env := &orakov1.Envelope{
		ProjectId: projectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_CONVERSATION_OPENED,
		Payload: &orakov1.Envelope_ConversationOpened{
			ConversationOpened: &orakov1.ConversationOpened{
				ConversationId: conversationID.String(),
				ProjectId:      projectID.String(),
				AskerMemberId:  uuid.NewString(),
				MemberId:       "",
				Question:       question,
			},
		},
	}

	payload, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	return message.NewMessage(uuid.NewString(), payload)
}

// slackSuccessServer returns an httptest server that ok's every Slack Web API
// call with a fixed channel/ts, suitable for chat.postMessage.
func slackSuccessServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"DSLACKCHAN","ts":"1111111111.000001"}`))
	}))
	t.Cleanup(srv.Close)

	return srv
}

// slackFailureServer returns an httptest server that fails every Slack Web
// API call.
func slackFailureServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"channel_not_found"}`))
	}))
	t.Cleanup(srv.Close)

	return srv
}

// telegramSuccessServer returns an httptest server that ok's every Telegram
// Bot API sendMessage call.
func telegramSuccessServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
	}))
	t.Cleanup(srv.Close)

	return srv
}

// slackMembersDouble/slackConvsDouble are minimal stand-ins for the Slack
// adapter's unexported dependencies; slack.Provider.Deliver resolves the
// recipient's SlackUserID via ByID internally, so it must be seeded with the
// same members the notifier already knows about.
type slackMembersDouble struct{ byID map[uuid.UUID]model.Member }

func (f *slackMembersDouble) ByID(_ context.Context, id uuid.UUID) (model.Member, error) {
	m, ok := f.byID[id]
	if !ok {
		return model.Member{}, adaptererr.ErrNotFound
	}

	return m, nil
}

func (f *slackMembersDouble) BySlackUserID(_ context.Context, _ string) (model.Member, error) {
	return model.Member{}, adaptererr.ErrNotFound
}

type slackConvsDouble struct{}

func (slackConvsDouble) ConversationBySlackThread(_ context.Context, _, _ string) (model.Conversation, error) {
	return model.Conversation{}, adaptererr.ErrNotFound
}

func (slackConvsDouble) SetSlackThread(_ context.Context, _ uuid.UUID, _, _ string) error { return nil }

type telegramMembersDouble struct{ byID map[uuid.UUID]model.Member }

func (f *telegramMembersDouble) ByID(_ context.Context, id uuid.UUID) (model.Member, error) {
	m, ok := f.byID[id]
	if !ok {
		return model.Member{}, adaptererr.ErrNotFound
	}

	return m, nil
}

func (f *telegramMembersDouble) ByTelegramChatID(_ context.Context, _ string) (model.Member, error) {
	return model.Member{}, adaptererr.ErrNotFound
}

type telegramConvsDouble struct{}

func (telegramConvsDouble) ConversationByTelegramMessage(_ context.Context, _, _ string) (model.Conversation, error) {
	return model.Conversation{}, adaptererr.ErrNotFound
}

func (telegramConvsDouble) SetTelegramThread(_ context.Context, _ uuid.UUID, _, _ string) error {
	return nil
}

// ── tests ────────────────────────────────────────────────────────────────────

// TestDeliveryNotifier_PoolFanOut_SlackTelegramDashboard is the phase-2 task 3
// acceptance criteria: a pool of 3 candidates (slack-bound, telegram-bound,
// dashboard/unbound) — the bound candidates each get a real provider DM
// (proven end-to-end through fake HTTP servers) and a ledger row in state
// 'posted'; the unbound candidate gets the email nudge instead.
func TestDeliveryNotifier_PoolFanOut_SlackTelegramDashboard(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	conversationID := uuid.New()

	slackMemberID := uuid.New()
	telegramMemberID := uuid.New()
	dashboardMemberID := uuid.New()

	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: slackMemberID, DisplayName: "Slack Sam", SlackUserID: "USLACK", DeliveryChannel: model.DeliveryChannelSlack})
	members.add(model.Member{ID: telegramMemberID, DisplayName: "Telegram Tia", TelegramChatID: "123456", DeliveryChannel: model.DeliveryChannelTelegram})
	members.add(model.Member{ID: dashboardMemberID, DisplayName: "Dana", Email: "dana@example.com", DeliveryChannel: model.DeliveryChannelDashboard})

	candidates := &fakeCandidatesReader{candidates: []model.Candidate{
		{MemberID: slackMemberID},
		{MemberID: telegramMemberID},
		{MemberID: dashboardMemberID},
	}}

	slackSrv := slackSuccessServer(t)
	slackProv := slack.New(slack.Config{BotToken: "xoxb-test", BaseURL: slackSrv.URL},
		&slackMembersDouble{byID: members.byID}, slackConvsDouble{}, nil)

	telegramSrv := telegramSuccessServer(t)
	telegramProv := telegram.New(telegram.Config{BotToken: "test-token", BaseURL: telegramSrv.URL},
		&telegramMembersDouble{byID: members.byID}, telegramConvsDouble{})

	providers := &fakeCandidateProviders{byMember: map[uuid.UUID]service.Provider{
		slackMemberID:    slackProv,
		telegramMemberID: telegramProv,
	}}

	ledger := &fakeLedgerWriter{}
	mailer := &fakeMailer{}

	h := DeliveryNotifier(members, candidates, providers, ledger, nil, mailer, nil, nil, "https://orako.example.com", quietLogger())
	if err := h(poolOpenedMsg(t, projectID, conversationID, "What is X?")); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if len(ledger.rows) != 2 {
		t.Fatalf("want 2 ledger rows (slack + telegram), got %d", len(ledger.rows))
	}

	for _, row := range ledger.rows {
		if row.ConversationID != conversationID {
			t.Errorf("ledger row conversation id = %v, want %v", row.ConversationID, conversationID)
		}

		if row.State != model.ProviderMessageStatePosted {
			t.Errorf("ledger row state = %q, want %q", row.State, model.ProviderMessageStatePosted)
		}
	}

	if len(mailer.sent) != 1 {
		t.Fatalf("want 1 email (the dashboard candidate), got %d", len(mailer.sent))
	}

	if mailer.sent[0].To != "dana@example.com" {
		t.Errorf("emailed To = %q, want dana@example.com", mailer.sent[0].To)
	}

	if len(members.updated) != 0 {
		t.Errorf("want no binding_error writes on the happy path, got %d", len(members.updated))
	}
}

// TestDeliveryNotifier_DeliverFailureFallsBackAndSetsBindingError proves
// per-candidate isolation: a failing Slack fake for one candidate falls back
// to emailing them and records binding_error, while a second, unrelated
// dashboard candidate is delivered normally and unaffected. It also proves
// the reserve→deliver→finalize tradeoff for a genuine provider-side Deliver
// failure (not a crash): the reservation written before Deliver ends up
// "failed", not stranded "reserving".
func TestDeliveryNotifier_DeliverFailureFallsBackAndSetsBindingError(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	conversationID := uuid.New()

	failingSlackMemberID := uuid.New()
	dashboardMemberID := uuid.New()

	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: failingSlackMemberID, DisplayName: "Broken Bob", Email: "bob@example.com", SlackUserID: "UBROKEN", DeliveryChannel: model.DeliveryChannelSlack})
	members.add(model.Member{ID: dashboardMemberID, DisplayName: "Dana", Email: "dana@example.com", DeliveryChannel: model.DeliveryChannelDashboard})

	candidates := &fakeCandidatesReader{candidates: []model.Candidate{
		{MemberID: failingSlackMemberID},
		{MemberID: dashboardMemberID},
	}}

	slackSrv := slackFailureServer(t)
	slackProv := slack.New(slack.Config{BotToken: "xoxb-test", BaseURL: slackSrv.URL},
		&slackMembersDouble{byID: members.byID}, slackConvsDouble{}, nil)

	providers := &fakeCandidateProviders{byMember: map[uuid.UUID]service.Provider{
		failingSlackMemberID: slackProv,
	}}

	ledger := &fakeLedgerWriter{}
	mailer := &fakeMailer{}

	h := DeliveryNotifier(members, candidates, providers, ledger, nil, mailer, nil, nil, "https://orako.example.com", quietLogger())
	if err := h(poolOpenedMsg(t, projectID, conversationID, "What is X?")); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if len(ledger.rows) != 1 {
		t.Fatalf("want 1 ledger row (the reservation written before the failed Deliver), got %d", len(ledger.rows))
	}

	if ledger.rows[0].State != model.ProviderMessageStateFailed {
		t.Errorf("row state = %q, want failed — a genuine Deliver failure must not leave the row reserving", ledger.rows[0].State)
	}

	if len(mailer.sent) != 2 {
		t.Fatalf("want 2 emails (fallback + dashboard candidate), got %d", len(mailer.sent))
	}

	failed, err := members.ByID(context.Background(), failingSlackMemberID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}

	if failed.BindingError == "" {
		t.Error("want binding_error set on the failing candidate, got empty")
	}

	other, err := members.ByID(context.Background(), dashboardMemberID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}

	if other.BindingError != "" {
		t.Errorf("dashboard candidate must be unaffected, got binding_error=%q", other.BindingError)
	}
}

// fakeFailingRoundTripper always fails at the transport level (never reaches
// a server), simulating a network/timeout failure. http.Client.Do wraps this
// into a *url.Error whose Error() string would otherwise embed the full
// request URL — for Telegram, ".../bot<TOKEN>/sendMessage".
type fakeFailingRoundTripper struct{}

func (fakeFailingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}

// TestDeliveryNotifier_TelegramTransportFailureDoesNotLeakBotToken proves the
// sink-side fix: a Telegram Deliver failure whose underlying transport error
// embeds a fake bot token must leave neither the token nor the request URL in
// binding_error (dashboard-visible) or in the application logs.
func TestDeliveryNotifier_TelegramTransportFailureDoesNotLeakBotToken(t *testing.T) {
	t.Parallel()

	const fakeToken = "999999:FAKE-LEAK-TOKEN"

	projectID := uuid.New()
	conversationID := uuid.New()
	memberID := uuid.New()

	members := newFakeMemberBindingWriter()
	members.add(model.Member{
		ID: memberID, DisplayName: "Casey", Email: "casey@example.com",
		TelegramChatID: "123", DeliveryChannel: model.DeliveryChannelTelegram,
	})

	candidates := &fakeCandidatesReader{candidates: []model.Candidate{{MemberID: memberID}}}

	telegramProv := telegram.New(telegram.Config{
		BotToken:   fakeToken,
		HTTPClient: &http.Client{Transport: fakeFailingRoundTripper{}},
	}, &telegramMembersDouble{byID: members.byID}, telegramConvsDouble{})

	providers := &fakeCandidateProviders{byMember: map[uuid.UUID]service.Provider{memberID: telegramProv}}
	ledger := &fakeLedgerWriter{}
	mailer := &fakeMailer{}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	h := DeliveryNotifier(members, candidates, providers, ledger, nil, mailer, nil, nil, "https://orako.example.com", logger)
	if err := h(poolOpenedMsg(t, projectID, conversationID, "What is X?")); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	failed, err := members.ByID(context.Background(), memberID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}

	if failed.BindingError == "" {
		t.Fatal("want binding_error set on the failing candidate, got empty")
	}

	if strings.Contains(failed.BindingError, fakeToken) {
		t.Errorf("binding_error must not leak the bot token, got: %q", failed.BindingError)
	}

	logged := logBuf.String()
	if strings.Contains(logged, fakeToken) {
		t.Errorf("logs must not leak the bot token, got: %s", logged)
	}

	if strings.Contains(logged, "http://") || strings.Contains(logged, "https://") {
		t.Errorf("logs must not leak the request URL, got: %s", logged)
	}
}

// TestDeliveryNotifier_ExcludedCandidateSkipped verifies a released
// (excluded) candidate is neither DMed nor emailed.
func TestDeliveryNotifier_ExcludedCandidateSkipped(t *testing.T) {
	t.Parallel()

	excludedAt := time.Now().UTC()
	memberID := uuid.New()

	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: memberID, Email: "excluded@example.com", DeliveryChannel: model.DeliveryChannelDashboard})

	candidates := &fakeCandidatesReader{candidates: []model.Candidate{
		{MemberID: memberID, ExcludedAt: &excludedAt},
	}}

	mailer := &fakeMailer{}

	h := DeliveryNotifier(members, candidates, &fakeCandidateProviders{}, &fakeLedgerWriter{}, nil, mailer, nil, nil, "https://orako.example.com", quietLogger())
	if err := h(poolOpenedMsg(t, uuid.New(), uuid.New(), "What is X?")); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if len(mailer.sent) != 0 {
		t.Errorf("want 0 emails for an excluded candidate, got %d", len(mailer.sent))
	}
}

// TestDeliveryNotifier_CandidateStoreFailureRetries proves a STORE-level
// failure (resolving the candidate list itself) returns an error so the
// router retries the whole message — distinct from a per-candidate Deliver
// failure, which never propagates.
func TestDeliveryNotifier_CandidateStoreFailureRetries(t *testing.T) {
	t.Parallel()

	candidates := &fakeCandidatesReader{err: errors.New("db unavailable")}

	h := DeliveryNotifier(newFakeMemberBindingWriter(), candidates, &fakeCandidateProviders{}, &fakeLedgerWriter{}, nil, &fakeMailer{}, nil, nil, "https://orako.example.com", quietLogger())
	if err := h(poolOpenedMsg(t, uuid.New(), uuid.New(), "What is X?")); err == nil {
		t.Fatal("want an error on candidate store failure, got nil")
	}
}

// TestDeliveryNotifier_DirectDashboardAskLeavesEmailToEmailNotifier verifies the
// delivery notifier owns direct external delivery but does not duplicate the
// dashboard email notifier.
func TestDeliveryNotifier_DirectDashboardAskLeavesEmailToEmailNotifier(t *testing.T) {
	t.Parallel()

	// A candidate-store call would return an error, proving it was never made.
	candidates := &fakeCandidatesReader{err: errors.New("must not be called for a direct ask")}
	responderID := uuid.New()
	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: responderID, DeliveryChannel: model.DeliveryChannelDashboard})

	env := &orakov1.Envelope{
		ProjectId: uuid.NewString(),
		Type:      orakov1.EventType_EVENT_TYPE_CONVERSATION_OPENED,
		Payload: &orakov1.Envelope_ConversationOpened{
			ConversationOpened: &orakov1.ConversationOpened{
				ConversationId: uuid.NewString(),
				ProjectId:      uuid.NewString(),
				AskerMemberId:  uuid.NewString(),
				MemberId:       responderID.String(),
				Question:       "direct ask",
			},
		},
	}

	payload, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	h := DeliveryNotifier(members, candidates, &fakeCandidateProviders{}, &fakeLedgerWriter{}, nil, &fakeMailer{}, nil, nil, "https://orako.example.com", quietLogger())
	if err := h(message.NewMessage(uuid.NewString(), payload)); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
}

func TestDeliveryNotifier_DirectExternalAskDeliversAsynchronously(t *testing.T) {
	t.Parallel()

	candidates := &fakeCandidatesReader{err: errors.New("must not be called for a direct ask")}
	projectID := uuid.New()
	conversationID := uuid.New()
	responderID := uuid.New()
	members := newFakeMemberBindingWriter()
	members.add(model.Member{
		ID:              responderID,
		DisplayName:     "Slack Sam",
		SlackUserID:     "USLACK",
		DeliveryChannel: model.DeliveryChannelSlack,
	})

	slackSrv := slackSuccessServer(t)
	slackProvider := slack.New(
		slack.Config{BotToken: "xoxb-test", BaseURL: slackSrv.URL},
		&slackMembersDouble{byID: members.byID},
		slackConvsDouble{},
		nil,
	)
	providers := &fakeCandidateProviders{byMember: map[uuid.UUID]service.Provider{
		responderID: slackProvider,
	}}
	ledger := &fakeLedgerWriter{}
	mailer := &fakeMailer{}
	env := &orakov1.Envelope{
		ProjectId: projectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_CONVERSATION_OPENED,
		Payload: &orakov1.Envelope_ConversationOpened{
			ConversationOpened: &orakov1.ConversationOpened{
				ConversationId: conversationID.String(),
				ProjectId:      projectID.String(),
				AskerMemberId:  uuid.NewString(),
				MemberId:       responderID.String(),
				Question:       "direct ask",
			},
		},
	}

	payload, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	h := DeliveryNotifier(
		members,
		candidates,
		providers,
		ledger,
		nil,
		mailer,
		nil,
		nil,
		"https://orako.example.com",
		quietLogger(),
	)
	if err := h(message.NewMessage(uuid.NewString(), payload)); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if len(ledger.rows) != 1 || ledger.rows[0].State != model.ProviderMessageStatePosted {
		t.Fatalf("direct external ledger rows = %+v, want one posted row", ledger.rows)
	}
	if len(mailer.sent) != 0 {
		t.Fatalf("direct external ask sent %d fallback emails, want 0", len(mailer.sent))
	}
}

// ── review #3-5: idempotent fan-out + transient-vs-poison discipline ───────

// TestDeliveryNotifier_DuplicateOpenIsIdempotent is the fix-#3 acceptance
// test: firing the exact same CONVERSATION_OPENED event twice against the
// same ledger (as an at-least-once redelivery would) must deliver exactly one
// DM per candidate — the second pass sees every candidate already has a
// ledger row and skips them all before ever calling Deliver.
func TestDeliveryNotifier_DuplicateOpenIsIdempotent(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	conversationID := uuid.New()
	slackMemberID := uuid.New()
	telegramMemberID := uuid.New()

	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: slackMemberID, DisplayName: "Slack Sam", SlackUserID: "USLACK", DeliveryChannel: model.DeliveryChannelSlack})
	members.add(model.Member{ID: telegramMemberID, DisplayName: "Telegram Tia", TelegramChatID: "123456", DeliveryChannel: model.DeliveryChannelTelegram})

	candidates := &fakeCandidatesReader{candidates: []model.Candidate{
		{MemberID: slackMemberID},
		{MemberID: telegramMemberID},
	}}

	slackSrv := slackSuccessServer(t)
	slackProv := slack.New(slack.Config{BotToken: "xoxb-test", BaseURL: slackSrv.URL},
		&slackMembersDouble{byID: members.byID}, slackConvsDouble{}, nil)

	telegramSrv := telegramSuccessServer(t)
	telegramProv := telegram.New(telegram.Config{BotToken: "test-token", BaseURL: telegramSrv.URL},
		&telegramMembersDouble{byID: members.byID}, telegramConvsDouble{})

	providers := &fakeCandidateProviders{byMember: map[uuid.UUID]service.Provider{
		slackMemberID:    slackProv,
		telegramMemberID: telegramProv,
	}}

	ledger := &fakeLedgerWriter{}
	mailer := &fakeMailer{}

	h := DeliveryNotifier(members, candidates, providers, ledger, nil, mailer, nil, nil, "https://orako.example.com", quietLogger())

	msg := poolOpenedMsg(t, projectID, conversationID, "What is X?")
	if err := h(msg); err != nil {
		t.Fatalf("first handler call returned error: %v", err)
	}

	if len(ledger.rows) != 2 {
		t.Fatalf("want 2 ledger rows after the first delivery, got %d", len(ledger.rows))
	}

	firstRows := append([]model.ProviderMessage(nil), ledger.rows...)

	// Redeliver the identical event — same conversation/candidates.
	if err := h(poolOpenedMsg(t, projectID, conversationID, "What is X?")); err != nil {
		t.Fatalf("second (duplicate) handler call returned error: %v", err)
	}

	if len(ledger.rows) != 2 {
		t.Fatalf("want still exactly 2 ledger rows after the duplicate OPEN, got %d", len(ledger.rows))
	}

	for i, row := range ledger.rows {
		if row != firstRows[i] {
			t.Errorf("ledger row %d changed across the duplicate OPEN: before=%+v after=%+v", i, firstRows[i], row)
		}
	}
}

// TestDeliveryNotifier_DuplicateOpenAfterClaimLeavesLedgerStateIntact proves
// the other half of fix #3's invariant: a duplicate OPEN arriving after the
// candidate's row already advanced past "posted" (e.g. claimed_won) must not
// regress it, and must not trigger a second Deliver.
func TestDeliveryNotifier_DuplicateOpenAfterClaimLeavesLedgerStateIntact(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	conversationID := uuid.New()
	memberID := uuid.New()

	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: memberID, DisplayName: "Slack Sam", SlackUserID: "USLACK", DeliveryChannel: model.DeliveryChannelSlack})

	candidates := &fakeCandidatesReader{candidates: []model.Candidate{{MemberID: memberID}}}

	slackSrv := slackSuccessServer(t)
	slackProv := slack.New(slack.Config{BotToken: "xoxb-test", BaseURL: slackSrv.URL},
		&slackMembersDouble{byID: members.byID}, slackConvsDouble{}, nil)

	providers := &fakeCandidateProviders{byMember: map[uuid.UUID]service.Provider{memberID: slackProv}}

	// Simulate the candidate's row already having been claimed before the
	// duplicate OPEN arrives.
	ledger := &fakeLedgerWriter{rows: []model.ProviderMessage{{
		ID: uuid.New(), ConversationID: conversationID, MemberID: memberID,
		ProviderKind: "slack", ChannelID: "DSLACKCHAN", MessageRef: "orig-ts",
		State: model.ProviderMessageStateClaimedWon,
	}}}
	mailer := &fakeMailer{}

	h := DeliveryNotifier(members, candidates, providers, ledger, nil, mailer, nil, nil, "https://orako.example.com", quietLogger())
	if err := h(poolOpenedMsg(t, projectID, conversationID, "What is X?")); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if len(ledger.rows) != 1 {
		t.Fatalf("want still exactly 1 ledger row, got %d", len(ledger.rows))
	}

	if ledger.rows[0].State != model.ProviderMessageStateClaimedWon {
		t.Errorf("ledger row state = %q, want claimed_won to survive the duplicate OPEN", ledger.rows[0].State)
	}

	if len(mailer.sent) != 0 {
		t.Errorf("want no email fallback for an already-claimed candidate, got %d", len(mailer.sent))
	}
}

// TestDeliveryNotifier_TransientMemberLookupFailureRetries proves the
// fix-#4 poison-vs-transient split: a members.ByID blip for one candidate is
// NOT a permanent drop — the handler returns an error so the router retries
// the whole message — while an unrelated, healthy candidate in the same pass
// is still delivered to.
func TestDeliveryNotifier_TransientMemberLookupFailureRetries(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	conversationID := uuid.New()

	brokenLookupMemberID := uuid.New()
	dashboardMemberID := uuid.New()

	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: dashboardMemberID, DisplayName: "Dana", Email: "dana@example.com", DeliveryChannel: model.DeliveryChannelDashboard})
	members.failByID(brokenLookupMemberID, errors.New("db unavailable"))

	candidates := &fakeCandidatesReader{candidates: []model.Candidate{
		{MemberID: brokenLookupMemberID},
		{MemberID: dashboardMemberID},
	}}

	ledger := &fakeLedgerWriter{}
	mailer := &fakeMailer{}

	h := DeliveryNotifier(members, candidates, &fakeCandidateProviders{}, ledger, nil, mailer, nil, nil, "https://orako.example.com", quietLogger())
	if err := h(poolOpenedMsg(t, projectID, conversationID, "What is X?")); err == nil {
		t.Fatal("want an error on a transient members.ByID failure, got nil")
	}

	if len(mailer.sent) != 1 || mailer.sent[0].To != "dana@example.com" {
		t.Errorf("want the healthy dashboard candidate still delivered in the same pass, got sent=%+v", mailer.sent)
	}
}

// TestDeliveryNotifier_ReservationFailureRetriesWithoutDelivering proves the
// reserve step's own store failure never reaches Deliver: nothing has been
// sent yet, so retrying the whole message is safe and cheap.
func TestDeliveryNotifier_ReservationFailureRetriesWithoutDelivering(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	conversationID := uuid.New()
	memberID := uuid.New()

	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: memberID, DisplayName: "Slack Sam", SlackUserID: "USLACK", DeliveryChannel: model.DeliveryChannelSlack})

	candidates := &fakeCandidatesReader{candidates: []model.Candidate{{MemberID: memberID}}}

	var deliverCount atomic.Int32

	slackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		deliverCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"DSLACKCHAN","ts":"1111111111.000001"}`))
	}))
	t.Cleanup(slackSrv.Close)

	slackProv := slack.New(slack.Config{BotToken: "xoxb-test", BaseURL: slackSrv.URL},
		&slackMembersDouble{byID: members.byID}, slackConvsDouble{}, nil)

	providers := &fakeCandidateProviders{byMember: map[uuid.UUID]service.Provider{memberID: slackProv}}
	ledger := &fakeLedgerWriter{err: errors.New("db unavailable")}
	mailer := &fakeMailer{}

	h := DeliveryNotifier(members, candidates, providers, ledger, nil, mailer, nil, nil, "https://orako.example.com", quietLogger())
	if err := h(poolOpenedMsg(t, projectID, conversationID, "What is X?")); err == nil {
		t.Fatal("want an error when the reserve write fails, got nil")
	}

	if got := deliverCount.Load(); got != 0 {
		t.Errorf("want Deliver never called when the reserve itself fails, got %d calls", got)
	}

	if len(ledger.rows) != 0 {
		t.Errorf("want 0 ledger rows (the reserve write never landed), got %d", len(ledger.rows))
	}

	failed, err := members.ByID(context.Background(), memberID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}

	if failed.BindingError != "" {
		t.Errorf("a ledger store failure must not stamp binding_error on a healthy binding, got %q", failed.BindingError)
	}
}

// TestDeliveryNotifier_FinalizeFailureAfterDeliverRetriesWithoutDuplicateSend
// is the two-phase reserve→deliver→finalize acceptance test: a Deliver that
// SUCCEEDS followed by a Finalize ledger write that FAILS must, on the
// router's whole-message retry, call Deliver EXACTLY ONCE net — the
// candidate's row already exists (reserving) after the first pass, so the
// notified-set upfront read skips them on the retry instead of re-entering
// Deliver. This is the fix for the double-delivery window: before this
// change, no row existed yet at the point Upsert failed, so a retry replayed
// Deliver a second time.
func TestDeliveryNotifier_FinalizeFailureAfterDeliverRetriesWithoutDuplicateSend(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	conversationID := uuid.New()
	memberID := uuid.New()

	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: memberID, DisplayName: "Slack Sam", SlackUserID: "USLACK", DeliveryChannel: model.DeliveryChannelSlack})

	candidates := &fakeCandidatesReader{candidates: []model.Candidate{{MemberID: memberID}}}

	var deliverCount atomic.Int32

	slackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		deliverCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"DSLACKCHAN","ts":"1111111111.000001"}`))
	}))
	t.Cleanup(slackSrv.Close)

	slackProv := slack.New(slack.Config{BotToken: "xoxb-test", BaseURL: slackSrv.URL},
		&slackMembersDouble{byID: members.byID}, slackConvsDouble{}, nil)

	providers := &fakeCandidateProviders{byMember: map[uuid.UUID]service.Provider{memberID: slackProv}}
	ledger := &fakeLedgerWriter{finalizeErr: errors.New("db unavailable")}
	mailer := &fakeMailer{}

	h := DeliveryNotifier(members, candidates, providers, ledger, nil, mailer, nil, nil, "https://orako.example.com", quietLogger())

	// First pass: the reserve lands, Deliver succeeds, the finalize fails.
	if err := h(poolOpenedMsg(t, projectID, conversationID, "What is X?")); err == nil {
		t.Fatal("want an error when the post-Deliver finalize fails, got nil")
	}

	if got := deliverCount.Load(); got != 1 {
		t.Fatalf("want Deliver called exactly once after the first pass, got %d", got)
	}

	if len(ledger.rows) != 1 {
		t.Fatalf("want exactly 1 reservation row after the first pass, got %d", len(ledger.rows))
	}

	if ledger.rows[0].State != model.ProviderMessageStateReserving {
		t.Fatalf("row state = %q, want reserving (finalize never landed)", ledger.rows[0].State)
	}

	// Router retry: the same event is redelivered. The candidate already has
	// a ledger row (reserving) — the notified-set upfront read must skip
	// them, so Deliver is NEVER called a second time, even though the row
	// never reached "posted".
	if err := h(poolOpenedMsg(t, projectID, conversationID, "What is X?")); err != nil {
		t.Fatalf("retry returned error: %v", err)
	}

	if got := deliverCount.Load(); got != 1 {
		t.Errorf("want Deliver still called exactly once net after the retry (no duplicate DM), got %d", got)
	}

	if len(ledger.rows) != 1 {
		t.Fatalf("want still exactly 1 ledger row after the retry, got %d", len(ledger.rows))
	}

	// Accepted crash-window tradeoff: the row stays "reserving" — the retry
	// never re-attempts the finalize either, since it never re-enters
	// deliverToCandidate for an already-reserved candidate.
	if ledger.rows[0].State != model.ProviderMessageStateReserving {
		t.Errorf("row state = %q, want it to stay reserving (the accepted crash-window tradeoff)", ledger.rows[0].State)
	}

	failed, err := members.ByID(context.Background(), memberID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}

	if failed.BindingError != "" {
		t.Errorf("a ledger store failure must not stamp binding_error on a healthy binding, got %q", failed.BindingError)
	}
}

// TestDeliveryNotifier_TransientProviderResolutionFailureRetries proves the
// fix-#4 resolver split: a transient providers.ForMember failure (anything
// other than service.ErrNoProvider) must retry the message rather than
// stamping binding_error on what may be a perfectly healthy binding.
func TestDeliveryNotifier_TransientProviderResolutionFailureRetries(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()

	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: memberID, DisplayName: "Slack Sam", SlackUserID: "USLACK", DeliveryChannel: model.DeliveryChannelSlack})

	candidates := &fakeCandidatesReader{candidates: []model.Candidate{{MemberID: memberID}}}

	providers := &fakeCandidateProviders{}
	providers.failByMember(memberID, errors.New("registry: transient lookup failure"))

	ledger := &fakeLedgerWriter{}
	mailer := &fakeMailer{}

	h := DeliveryNotifier(members, candidates, providers, ledger, nil, mailer, nil, nil, "https://orako.example.com", quietLogger())
	if err := h(poolOpenedMsg(t, uuid.New(), uuid.New(), "What is X?")); err == nil {
		t.Fatal("want an error on a transient provider-resolution failure, got nil")
	}

	failed, err := members.ByID(context.Background(), memberID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}

	if failed.BindingError != "" {
		t.Errorf("a transient resolver failure must not stamp binding_error, got %q", failed.BindingError)
	}

	if len(mailer.sent) != 0 {
		t.Errorf("a transient resolver failure must not fall back to email either (it retries instead), got %d", len(mailer.sent))
	}
}

// TestDeliveryNotifier_NoProviderConfiguredFallsBackWithoutRetry proves the
// permanent half of the same split: service.ErrNoProvider (the project has
// no provider configured at all) is not transient — retrying forever cannot
// fix a missing configuration — so it falls back to email like an unbound
// candidate and does not fail the message.
func TestDeliveryNotifier_NoProviderConfiguredFallsBackWithoutRetry(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()

	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: memberID, DisplayName: "Slack Sam", Email: "sam@example.com", SlackUserID: "USLACK", DeliveryChannel: model.DeliveryChannelSlack})

	candidates := &fakeCandidatesReader{candidates: []model.Candidate{{MemberID: memberID}}}

	providers := &fakeCandidateProviders{}
	providers.failByMember(memberID, service.ErrNoProvider)

	ledger := &fakeLedgerWriter{}
	mailer := &fakeMailer{}

	h := DeliveryNotifier(members, candidates, providers, ledger, nil, mailer, nil, nil, "https://orako.example.com", quietLogger())
	if err := h(poolOpenedMsg(t, uuid.New(), uuid.New(), "What is X?")); err != nil {
		t.Fatalf("want no error for ErrNoProvider (permanent, falls back instead), got: %v", err)
	}

	if len(mailer.sent) != 1 || mailer.sent[0].To != "sam@example.com" {
		t.Errorf("want the email fallback for the unconfigured project, got sent=%+v", mailer.sent)
	}

	failed, err := members.ByID(context.Background(), memberID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}

	if failed.BindingError != "" {
		t.Errorf("ErrNoProvider is a project-level condition, not the member's binding — must not stamp binding_error, got %q", failed.BindingError)
	}
}

// TestDeliveryNotifier_ThreadCoveredCandidateSkipsDM is phase-4 fan-out
// preemption at ask time: a candidate covered by the conversation's Discord
// thread surface gets NO question DM and no ledger row (the thread carries
// the question); an uncovered candidate is delivered as usual.
func TestDeliveryNotifier_ThreadCoveredCandidateSkipsDM(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	conversationID := uuid.New()

	coveredID := uuid.New()
	slackMemberID := uuid.New()

	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: coveredID, DisplayName: "Barbara", Email: "b@example.com", DiscordUserID: "d-barbara", DeliveryChannel: model.DeliveryChannelDiscord})
	members.add(model.Member{ID: slackMemberID, DisplayName: "Sam", Email: "sam@example.com", SlackUserID: "U123", DeliveryChannel: model.DeliveryChannelSlack})

	candidates := &fakeCandidatesReader{candidates: []model.Candidate{
		{MemberID: coveredID},
		{MemberID: slackMemberID},
	}}

	slackSrv := slackSuccessServer(t)
	slackProv := slack.New(slack.Config{BotToken: "xoxb-test", BaseURL: slackSrv.URL},
		&slackMembersDouble{byID: members.byID}, slackConvsDouble{}, nil)

	providers := &fakeCandidateProviders{byMember: map[uuid.UUID]service.Provider{
		slackMemberID: slackProv,
	}}

	// A pre-existing surface covering Barbara (EnsureDiscordThread's replay
	// short-circuit adopts it).
	store := newFakeSurfaceStore()
	store.byConv[conversationID] = model.ConversationSurface{
		ID: uuid.New(), ConversationID: conversationID,
		Provider: model.SurfaceProviderDiscord, Kind: model.SurfaceKindThread,
		ChannelID: "chan", ThreadID: "thread-1",
		CoveredMemberIDs: []uuid.UUID{coveredID},
	}
	surfaces := newTestSurfaceManager(store, newFakeThreadProvider(), "chan",
		model.Conversation{ID: conversationID, ProjectID: projectID, AskerMemberID: uuid.New(), Question: "Q?", Status: model.ConversationStatusOpen},
		&fakeFanoutMembers{names: map[uuid.UUID]string{}})

	ledger := &fakeLedgerWriter{}
	mailer := &fakeMailer{}

	h := DeliveryNotifier(members, candidates, providers, ledger, surfaces, mailer, nil, nil, "https://orako.example.com", quietLogger())
	if err := h(poolOpenedMsg(t, projectID, conversationID, "What is X?")); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if len(ledger.rows) != 1 || ledger.rows[0].MemberID != slackMemberID {
		t.Fatalf("want exactly one ledger row (Sam), got %+v", ledger.rows)
	}

	if len(mailer.sent) != 0 {
		t.Errorf("the covered candidate must not be emailed either, got %d", len(mailer.sent))
	}
}

// fakeAttachReader returns a fixed set of a conversation's attachments.
type fakeAttachReader struct{ atts []model.Attachment }

func (f fakeAttachReader) ByConversation(_ context.Context, _ uuid.UUID) ([]model.Attachment, error) {
	return f.atts, nil
}

// fakeSigner mints a deterministic signed URL from the blob key.
type fakeSigner struct{ enabled bool }

func (f fakeSigner) SignedGetURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://signed/" + key, nil
}

func (f fakeSigner) Enabled() bool { return f.enabled }

// TestDeliveryNotifier_PoolDeliversAttachments proves a pool dispatch carries
// the opening question's attachment (signed) into each candidate's DM — closing
// the gap where a pool ask reached candidates with text but no image.
func TestDeliveryNotifier_PoolDeliversAttachments(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	conversationID := uuid.New()
	memberID := uuid.New()

	members := newFakeMemberBindingWriter()
	members.add(model.Member{
		ID: memberID, DisplayName: "Casey", Email: "casey@example.com",
		DiscordUserID: "discord-1", DeliveryChannel: model.DeliveryChannelDiscord,
	})

	candidates := &fakeCandidatesReader{candidates: []model.Candidate{{MemberID: memberID}}}
	prov := &fakeEditorProvider{}
	providers := &fakeCandidateProviders{byMember: map[uuid.UUID]service.Provider{memberID: prov}}

	atts := fakeAttachReader{atts: []model.Attachment{{
		ID: uuid.New(), MessageID: uuid.New(), Filename: "shot.png",
		MimeType: "image/png", SizeBytes: 5, StorageKey: "k1",
	}}}

	h := DeliveryNotifier(members, candidates, providers, &fakeLedgerWriter{}, nil, &fakeMailer{},
		atts, fakeSigner{enabled: true}, "https://orako.example.com", quietLogger())

	if err := h(poolOpenedMsg(t, projectID, conversationID, "look at this error")); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if len(prov.delivered) != 1 {
		t.Fatalf("want one delivery, got %d", len(prov.delivered))
	}

	got := prov.delivered[0].Attachments
	if len(got) != 1 || got[0].Filename != "shot.png" || got[0].URL != "https://signed/k1" {
		t.Fatalf("delivered attachments = %+v, want shot.png signed to k1", got)
	}
}
