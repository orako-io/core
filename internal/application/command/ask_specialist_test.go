// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/errs"
)

// newAskHandler is a convenience constructor for tests.
func newAskHandler(
	opener *fakeConversationOpener,
	convRepo *fakeConversationRepository,
	bus *fakeEventBus,
	lookup *fakeProviderLookup,
) (AskHandler, *fakeLedgerWriter) {
	ledger := &fakeLedgerWriter{}

	return MustNewAskHandler(opener, convRepo, bus, lookup, &fakeCandidatePool{}, alwaysDashboardMembers{}, newFakeProjectRepo(), fakeTransactor{}, nil), ledger
}

// recordingProvider captures the last OutboundMessage it was asked to deliver.
type recordingProvider struct {
	last service.OutboundMessage
}

func (p *recordingProvider) Deliver(_ context.Context, msg service.OutboundMessage) (service.MessageRef, error) {
	p.last = msg

	return service.MessageRef{ChannelID: "dm-1", MessageID: "msg-1"}, nil
}

func (p *recordingProvider) ParseInbound(_ context.Context, _ []byte) (service.InboundMessage, error) {
	return service.InboundMessage{}, service.ErrUnrecognizedMessage
}

// fakeAskAttachments is an askAttachmentStore: it resolves seeded rows by id and
// records which ids were linked to which message.
type fakeAskAttachments struct {
	byID     map[uuid.UUID]model.Attachment
	linked   []uuid.UUID
	linkedTo uuid.UUID
}

func (f *fakeAskAttachments) LinkToMessage(_ context.Context, _, messageID uuid.UUID, ids []uuid.UUID) (int64, error) {
	f.linked = append(f.linked, ids...)
	f.linkedTo = messageID

	return int64(len(ids)), nil
}

func (f *fakeAskAttachments) ByID(_ context.Context, id uuid.UUID) (model.Attachment, error) {
	a, ok := f.byID[id]
	if !ok {
		return model.Attachment{}, errors.New("attachment not found")
	}

	return a, nil
}

// TestAsk_DirectAttachmentQueued proves the attachment is linked before the
// opening event is committed; asynchronous delivery resolves it from storage.
func TestAsk_DirectAttachmentQueued(t *testing.T) {
	t.Parallel()

	prov := &recordingProvider{}
	lookup := newFakeProviderLookup(prov)

	attID := uuid.New()
	atts := &fakeAskAttachments{
		byID: map[uuid.UUID]model.Attachment{
			attID: {ID: attID, Filename: "shot.png", MimeType: "image/png", SizeBytes: 12, StorageKey: "proj/conv/att/shot.png"},
		},
	}
	h := MustNewAskHandler(
		newFakeConversationOpener(), newFakeConvRepo(), &fakeEventBus{}, lookup,
		&fakeCandidatePool{}, alwaysDashboardMembers{}, newFakeProjectRepo(),
		fakeTransactor{}, atts,
	)

	_, err := h.Handle(t.Context(), AskCommand{
		Summary:           "test summary",
		Tags:              []string{"topic"},
		ProjectID:         uuid.New(),
		AskerMemberID:     uuid.New(),
		ResponderMemberID: uuid.New(),
		Question:          "what's this error?",
		Context:           "ctx",
		AttachmentIDs:     []uuid.UUID{attID},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(atts.linked) != 1 || atts.linked[0] != attID || atts.linkedTo == uuid.Nil {
		t.Fatalf("attachment not linked to opening message: linked=%v to=%v", atts.linked, atts.linkedTo)
	}

	if len(prov.last.Attachments) != 0 {
		t.Fatalf("command performed inline delivery: %+v", prov.last.Attachments)
	}
}

// TestAsk_RejectsEmptySummary proves the required-but-guided metadata contract:
// an ask with no summary is rejected (pre-write) with a guidance-bearing
// errs.InvalidError, never a silent default.
func TestAsk_RejectsEmptySummary(t *testing.T) {
	t.Parallel()

	convOpener := newFakeConversationOpener()
	convRepo := newFakeConvRepo()
	bus := &fakeEventBus{}
	lookup := newFakeProviderLookup(&noopProvider{})

	h, _ := newAskHandler(convOpener, convRepo, bus, lookup)

	_, err := h.Handle(t.Context(), AskCommand{
		Tags:              []string{"topic"},
		ProjectID:         uuid.New(),
		AskerMemberID:     uuid.New(),
		ResponderMemberID: uuid.New(),
		Question:          "What does orako do?",
		Context:           "agent context here",
	})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) || invalid.Field != "summary" {
		t.Fatalf("want InvalidError on summary, got %v", err)
	}

	if len(convOpener.conversations) != 0 {
		t.Error("no conversation must be opened when metadata is rejected")
	}
}

// TestAsk_RejectsNoTags proves an ask with a summary but zero tags is rejected.
func TestAsk_RejectsNoTags(t *testing.T) {
	t.Parallel()

	convOpener := newFakeConversationOpener()
	convRepo := newFakeConvRepo()
	bus := &fakeEventBus{}
	lookup := newFakeProviderLookup(&noopProvider{})

	h, _ := newAskHandler(convOpener, convRepo, bus, lookup)

	_, err := h.Handle(t.Context(), AskCommand{
		Summary:           "a summary but no tags",
		ProjectID:         uuid.New(),
		AskerMemberID:     uuid.New(),
		ResponderMemberID: uuid.New(),
		Question:          "What does orako do?",
		Context:           "agent context here",
	})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) || invalid.Field != "tags" {
		t.Fatalf("want InvalidError on tags, got %v", err)
	}

	if len(convOpener.conversations) != 0 {
		t.Error("no conversation must be opened when metadata is rejected")
	}
}

func TestAsk_HappyPath(t *testing.T) {
	t.Parallel()

	convOpener := newFakeConversationOpener()
	convRepo := newFakeConvRepo()
	bus := &fakeEventBus{}
	lookup := newFakeProviderLookup(&noopProvider{})

	h, _ := newAskHandler(convOpener, convRepo, bus, lookup)

	projectID := uuid.New()
	askerID := uuid.New()
	targetID := uuid.New()

	result, err := h.Handle(t.Context(), AskCommand{
		Summary:           "test summary",
		Tags:              []string{"topic"},
		ProjectID:         projectID,
		AskerMemberID:     askerID,
		ResponderMemberID: targetID,
		Question:          "What does orako do?",
		Context:           "agent context here",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if result.ConversationID == uuid.Nil {
		t.Fatal("Handle returned nil conversation ID")
	}

	// Conversation stored atomically.
	conv, ok := convOpener.conversations[result.ConversationID]
	if !ok {
		t.Fatal("conversation not stored in opener")
	}

	if conv.Status != model.ConversationStatusOpen {
		t.Errorf("Status = %q, want open", conv.Status)
	}

	// Question message stored atomically.
	msgs := convOpener.messages[result.ConversationID]
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}

	if msgs[0].Role != model.MessageRoleQuestion {
		t.Errorf("message role = %v, want question", msgs[0].Role)
	}

	// Events published.
	if bus.countOfType(orakov1.EventType_EVENT_TYPE_CONVERSATION_OPENED) != 1 {
		t.Error("ConversationOpened event not published")
	}

	if bus.countOfType(orakov1.EventType_EVENT_TYPE_MESSAGE_POSTED) != 1 {
		t.Error("MessagePosted event not published")
	}
}

// TestAsk_DirectAsk_QueuesDelivery proves direct delivery is represented by the
// durable opening event and is no longer attempted inline by the command.
func TestAsk_DirectAsk_QueuesDelivery(t *testing.T) {
	t.Parallel()

	convOpener := newFakeConversationOpener()
	bus := &fakeEventBus{}
	lookup := newFakeProviderLookup(&noopProvider{})

	h, _ := newAskHandler(convOpener, newFakeConvRepo(), bus, lookup)

	targetID := uuid.New()

	_, err := h.Handle(t.Context(), AskCommand{
		Summary:           "test summary",
		Tags:              []string{"topic"},
		ProjectID:         uuid.New(),
		AskerMemberID:     uuid.New(),
		ResponderMemberID: targetID,
		Question:          "What does orako do?",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	opened, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_CONVERSATION_OPENED)
	if !ok || opened.GetConversationOpened().GetMemberId() != targetID.String() {
		t.Fatalf("direct opening event does not target %s", targetID)
	}
}

func TestAsk_PoolAsk_QueuesPoolDelivery(t *testing.T) {
	t.Parallel()

	members := []uuid.UUID{uuid.New(), uuid.New()}
	convOpener := newFakeConversationOpener()
	bus := &fakeEventBus{}
	// noProvider guards the path: a pool dispatch must not consult it.
	lookup := &fakeProviderLookup{noProvider: true}
	h := MustNewAskHandler(convOpener, newFakeConvRepo(), bus, lookup,
		&fakeCandidatePool{members: members}, alwaysDashboardMembers{}, newFakeProjectRepo(), fakeTransactor{}, nil)

	_, err := h.Handle(t.Context(), AskCommand{
		Summary:       "test summary",
		Tags:          []string{"topic"},
		ProjectID:     uuid.New(),
		AskerMemberID: uuid.New(),
		Domains:       []string{"database"},
		Question:      "Which index fits this query?",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	opened, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_CONVERSATION_OPENED)
	if !ok || opened.GetConversationOpened().GetMemberId() != "" {
		t.Fatal("pool opening event must carry an empty responder")
	}
}

func TestAsk_EmptyQuestion(t *testing.T) {
	t.Parallel()

	h, _ := newAskHandler(
		newFakeConversationOpener(),
		newFakeConvRepo(),
		&fakeEventBus{},
		newFakeProviderLookup(&noopProvider{}),
	)

	_, err := h.Handle(t.Context(), AskCommand{
		Summary:       "test summary",
		Tags:          []string{"topic"},
		ProjectID:     uuid.New(),
		AskerMemberID: uuid.New(),
		Question:      "",
	})
	if err == nil {
		t.Fatal("expected error for empty question")
	}

	var inv errs.InvalidError
	if !errors.As(err, &inv) {
		t.Errorf("want InvalidError, got %T: %v", err, err)
	}
}

func TestAsk_ProviderDeliveryIsNotOnRequestPath(t *testing.T) {
	t.Parallel()

	lookup := newFakeProviderLookup(&noopProvider{deliverErr: errs.InternalError{}})

	h, _ := newAskHandler(
		newFakeConversationOpener(),
		newFakeConvRepo(),
		&fakeEventBus{},
		lookup,
	)

	_, err := h.Handle(t.Context(), AskCommand{
		Summary:           "test summary",
		Tags:              []string{"topic"},
		ProjectID:         uuid.New(),
		AskerMemberID:     uuid.New(),
		ResponderMemberID: uuid.New(),
		Question:          "What?",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
}

// TestAsk_NoProviderConfigured verifies that when no messaging
// provider is configured for the project the handler returns a clean
// InvalidError without writing any conversation, message, or events.
func TestAsk_NoProviderConfigured(t *testing.T) {
	t.Parallel()

	convOpener := newFakeConversationOpener()
	bus := &fakeEventBus{}

	lookup := newFakeProviderLookup(nil)
	lookup.noProvider = true

	h, _ := newAskHandler(convOpener, newFakeConvRepo(), bus, lookup)

	_, err := h.Handle(t.Context(), AskCommand{
		Summary:           "test summary",
		Tags:              []string{"topic"},
		ProjectID:         uuid.New(),
		AskerMemberID:     uuid.New(),
		ResponderMemberID: uuid.New(),
		Question:          "Will this orphan?",
	})
	if err == nil {
		t.Fatal("expected error when no provider configured")
	}

	var inv errs.InvalidError
	if !errors.As(err, &inv) {
		t.Errorf("want InvalidError, got %T: %v", err, err)
	}

	// No store calls: the opener must be empty.
	if len(convOpener.conversations) != 0 {
		t.Errorf("convOpener.conversations = %d, want 0 (no orphan)", len(convOpener.conversations))
	}

	// No events published.
	if len(bus.published) != 0 {
		t.Errorf("bus.published = %d, want 0", len(bus.published))
	}
}

// TestAsk_Wait_AnswerArrivesInline verifies that when wait=true the
// handler always polls for an ANSWER-role message (presence is no longer
// consulted — a responder can claim from a phone at any moment) and returns
// it inline with Answered=true as soon as one is posted.
func TestAsk_Wait_AnswerArrivesInline(t *testing.T) {
	t.Parallel()

	// Seed the read repo with a pending answer body.  MessagesByConversation
	// returns this synthetic message for any conversation ID so the poll
	// succeeds on the first iteration regardless of the generated UUID.
	convRepo := newFakeConvRepo()
	convRepo.pendingAnswerBody = "Orako routes questions to experts."

	targetID := uuid.New()

	h, _ := newAskHandler(
		newFakeConversationOpener(),
		convRepo,
		&fakeEventBus{},
		newFakeProviderLookup(&noopProvider{}),
	)

	result, err := h.Handle(t.Context(), AskCommand{
		Summary:           "test summary",
		Tags:              []string{"topic"},
		ProjectID:         uuid.New(),
		AskerMemberID:     uuid.New(),
		ResponderMemberID: targetID,
		Question:          "What does orako do?",
		Wait:              true,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if !result.Answered {
		t.Error("Answered = false, want true (inline answer path)")
	}

	if result.InlineAnswer != convRepo.pendingAnswerBody {
		t.Errorf("InlineAnswer = %q, want %q", result.InlineAnswer, convRepo.pendingAnswerBody)
	}
}

// TestAsk_Wait_TimesOutToAsync verifies that when wait=true and no
// answer arrives before the caller's context is done, the handler falls back
// to the async path (Answered=false) without returning an error. Presence is
// irrelevant to this contract: wait always polls, timeout is driven purely by
// whether an answer was posted in time.
func TestAsk_Wait_TimesOutToAsync(t *testing.T) {
	t.Parallel()

	targetID := uuid.New()

	h, _ := newAskHandler(
		newFakeConversationOpener(),
		newFakeConvRepo(), // no pending answer — poll never succeeds
		&fakeEventBus{},
		newFakeProviderLookup(&noopProvider{}),
	)

	// Short-lived context so the poll's select picks ctx.Done() long before the
	// real 90s wait window would elapse.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	result, err := h.Handle(ctx, AskCommand{
		Summary:           "test summary",
		Tags:              []string{"topic"},
		ProjectID:         uuid.New(),
		AskerMemberID:     uuid.New(),
		ResponderMemberID: targetID,
		Question:          "Will this time out?",
		Wait:              true,
	})
	if err != nil {
		t.Fatalf("Handle returned unexpected error: %v", err)
	}

	if result.Answered {
		t.Error("Answered = true, want false (async fallback path)")
	}

	if result.ConversationID == uuid.Nil {
		t.Error("ConversationID is nil; async path must still return a valid ID")
	}
}

// TestAsk_PoolWait_AnswerArrivesInline proves that a pool dispatch
// with wait=true polls for an inline answer exactly like a direct ask — the
// candidate's channel binding (Slack here) has no bearing on whether the wait
// happens: wait is no longer gated on presence or reachability at all.
func TestAsk_PoolWait_AnswerArrivesInline(t *testing.T) {
	t.Parallel()

	candidateID := uuid.New()

	members := newFakeMemberBindingReader()
	members.add(model.Member{ID: candidateID, SlackUserID: "UCANDIDATE", DeliveryChannel: model.DeliveryChannelSlack})

	convRepo := newFakeConvRepo()
	convRepo.pendingAnswerBody = "answered via slack"

	h := MustNewAskHandler(
		newFakeConversationOpener(),
		convRepo,
		&fakeEventBus{},
		&fakeProviderLookup{noProvider: true}, // pool dispatch must not consult a direct provider
		&fakeCandidatePool{members: []uuid.UUID{candidateID}},
		members,
		newFakeProjectRepo(),
		fakeTransactor{}, nil,
	)

	result, err := h.Handle(t.Context(), AskCommand{
		Summary:       "test summary",
		Tags:          []string{"topic"},
		ProjectID:     uuid.New(),
		AskerMemberID: uuid.New(),
		Domains:       []string{"database"},
		Question:      "Which index fits this query?",
		Wait:          true,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if !result.Answered {
		t.Error("Answered = false, want true — a pool wait must poll for an inline answer")
	}

	if result.InlineAnswer != convRepo.pendingAnswerBody {
		t.Errorf("InlineAnswer = %q, want %q", result.InlineAnswer, convRepo.pendingAnswerBody)
	}
}

// TestAsk_PoolWait_TimesOutToAsync proves a pool dispatch with
// wait=true falls back to the async path (Answered=false) when no answer
// arrives before the caller's context is done — the candidate's channel
// binding (dashboard here) has no bearing on the outcome.
func TestAsk_PoolWait_TimesOutToAsync(t *testing.T) {
	t.Parallel()

	candidateID := uuid.New()

	members := newFakeMemberBindingReader()
	members.add(model.Member{ID: candidateID, DeliveryChannel: model.DeliveryChannelDashboard})

	convRepo := newFakeConvRepo() // no pending answer — poll never succeeds

	h := MustNewAskHandler(
		newFakeConversationOpener(),
		convRepo,
		&fakeEventBus{},
		&fakeProviderLookup{noProvider: true},
		&fakeCandidatePool{members: []uuid.UUID{candidateID}},
		members,
		newFakeProjectRepo(),
		fakeTransactor{}, nil,
	)

	// Short-lived context so the poll's select picks ctx.Done() long before the
	// real 90s wait window would elapse.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	result, err := h.Handle(ctx, AskCommand{
		Summary:       "test summary",
		Tags:          []string{"topic"},
		ProjectID:     uuid.New(),
		AskerMemberID: uuid.New(),
		Domains:       []string{"database"},
		Question:      "Which index fits this query?",
		Wait:          true,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if result.Answered {
		t.Error("Answered = true, want false — no answer was posted before the context deadline")
	}

	if result.ConversationID == uuid.Nil {
		t.Error("ConversationID is nil; async path must still return a valid ID")
	}
}

// TestAsk_EnrichesNames proves a direct ask echoes the resolved
// project name and the target's display name on the result.
func TestAsk_EnrichesNames(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	targetID := uuid.New()

	projects := newFakeProjectRepo()
	projects.projects[projectID] = model.Project{ID: projectID, Name: "cfea"}

	members := newFakeMemberBindingReader()
	members.add(model.Member{ID: targetID, DisplayName: "Marc", DeliveryChannel: model.DeliveryChannelDashboard, Status: model.MemberStatusActive})

	h := MustNewAskHandler(
		newFakeConversationOpener(), newFakeConvRepo(), &fakeEventBus{},
		newFakeProviderLookup(&noopProvider{}), &fakeCandidatePool{}, members, projects,
		fakeTransactor{}, nil,
	)

	result, err := h.Handle(t.Context(), AskCommand{
		Summary:           "test summary",
		Tags:              []string{"topic"},
		ProjectID:         projectID,
		AskerMemberID:     uuid.New(),
		ResponderMemberID: targetID,
		Question:          "Which index fits this query?",
		Context:           "ctx",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if result.ProjectName != "cfea" {
		t.Errorf("ProjectName = %q, want %q", result.ProjectName, "cfea")
	}

	if len(result.RecipientNames) != 1 || result.RecipientNames[0] != "Marc" {
		t.Errorf("RecipientNames = %v, want [Marc]", result.RecipientNames)
	}
}

// TestAsk_NameResolutionFailureIsCosmetic proves an unresolvable
// project name leaves ProjectName empty but never fails the ask. The responder
// exists and is active (a direct ask requires that); only the project lookup
// misses here.
func TestAsk_NameResolutionFailureIsCosmetic(t *testing.T) {
	t.Parallel()

	targetID := uuid.New()
	members := newFakeMemberBindingReader()
	members.add(model.Member{ID: targetID, DeliveryChannel: model.DeliveryChannelDashboard, Status: model.MemberStatusActive})

	// Unseeded project repo: the project name lookup returns ErrNotFound.
	h := MustNewAskHandler(
		newFakeConversationOpener(), newFakeConvRepo(), &fakeEventBus{},
		newFakeProviderLookup(&noopProvider{}), &fakeCandidatePool{},
		members, newFakeProjectRepo(),
		fakeTransactor{}, nil,
	)

	result, err := h.Handle(t.Context(), AskCommand{
		Summary:           "test summary",
		Tags:              []string{"topic"},
		ProjectID:         uuid.New(),
		AskerMemberID:     uuid.New(),
		ResponderMemberID: targetID,
		Question:          "still works?",
		Context:           "ctx",
	})
	if err != nil {
		t.Fatalf("Handle must not fail on a project name-lookup miss: %v", err)
	}

	if result.ConversationID == uuid.Nil {
		t.Fatal("conversation should still be opened")
	}

	if result.ProjectName != "" {
		t.Errorf("expected empty project name on a lookup miss, got %q", result.ProjectName)
	}
}

// TestAsk_DirectToUnroutableIsRejected proves a direct ask to an
// on-leave or deactivated responder is refused before any write, with an
// actionable reroute message.
func TestAsk_DirectToUnroutableIsRejected(t *testing.T) {
	t.Parallel()

	for _, st := range []model.MemberStatus{model.MemberStatusOnLeave, model.MemberStatusDeactivated} {
		targetID := uuid.New()
		members := newFakeMemberBindingReader()
		members.add(model.Member{ID: targetID, DeliveryChannel: model.DeliveryChannelDashboard, Status: st})

		convOpener := newFakeConversationOpener()
		h := MustNewAskHandler(
			convOpener, newFakeConvRepo(), &fakeEventBus{},
			newFakeProviderLookup(&noopProvider{}), &fakeCandidatePool{}, members, newFakeProjectRepo(),
			fakeTransactor{}, nil,
		)

		_, err := h.Handle(t.Context(), AskCommand{
			Summary:           "test summary",
			Tags:              []string{"topic"},
			ProjectID:         uuid.New(),
			AskerMemberID:     uuid.New(),
			ResponderMemberID: targetID,
			Question:          "still there?",
			Context:           "ctx",
		})

		var invalid errs.InvalidError
		if !errors.As(err, &invalid) {
			t.Fatalf("status %q: want InvalidError, got %v", st, err)
		}

		if len(convOpener.conversations) != 0 {
			t.Errorf("status %q: no conversation should be opened for an unroutable target", st)
		}
	}
}

// Compile-time check: noopProvider satisfies service.Provider.
var _ service.Provider = (*noopProvider)(nil)

// TestAsk_SelfAskAllowed proves an agent may route a question to
// its OWN user (responder == asker): the direct ask passes validation, the
// conversation opens with the caller as both sides, and the delivery ledger
// records the DM to them — the self-ask loop's outbound half.
func TestAsk_SelfAskAllowed(t *testing.T) {
	t.Parallel()

	convOpener := newFakeConversationOpener()
	bus := &fakeEventBus{}
	lookup := newFakeProviderLookup(&noopProvider{})

	h, _ := newAskHandler(convOpener, newFakeConvRepo(), bus, lookup)

	me := uuid.New()

	result, err := h.Handle(t.Context(), AskCommand{
		Summary:           "test summary",
		Tags:              []string{"topic"},
		ProjectID:         uuid.New(),
		AskerMemberID:     me,
		ResponderMemberID: me,
		Question:          "Ton agent a besoin d'une décision : on unifie les seats côté serveur ?",
	})
	if err != nil {
		t.Fatalf("Handle(self-ask): %v", err)
	}

	opened, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_CONVERSATION_OPENED)
	if !ok || opened.GetConversationOpened().GetMemberId() != me.String() {
		t.Fatalf("self-ask opening event must target the asker")
	}

	if result.ConversationID == uuid.Nil {
		t.Error("self-ask must open a real conversation")
	}
}
