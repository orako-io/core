// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// ---- pool dispatch (Ask with domains) -----------------------------

// TestAsk_PoolDispatch proves a domains ask opens an unlabeled
// conversation with its candidate set, reports the pool size, and never
// consults the direct-ask provider path.
func TestAsk_PoolDispatch(t *testing.T) {
	t.Parallel()

	members := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	convOpener := newFakeConversationOpener()
	bus := &fakeEventBus{}
	// noProvider guards the path: a pool dispatch must not look up a provider,
	// so this lookup would fail the ask if it were consulted.
	lookup := &fakeProviderLookup{noProvider: true}

	h := MustNewAskHandler(convOpener, newFakeConvRepo(), bus, lookup,
		&fakeCandidatePool{members: members}, alwaysDashboardMembers{}, newFakeProjectRepo(), fakeTransactor{}, nil)

	result, err := h.Handle(t.Context(), AskCommand{
		Summary:       "test summary",
		Tags:          []string{"topic"},
		ProjectID:     uuid.New(),
		AskerMemberID: uuid.New(),
		Domains:       []string{"database", "infra"},
		Question:      "Which index fits this query?",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if result.PoolSize != len(members) {
		t.Errorf("PoolSize = %d, want %d", result.PoolSize, len(members))
	}

	conv := convOpener.conversations[result.ConversationID]
	if conv.ResponderMemberID != uuid.Nil {
		t.Errorf("pool conversation must open without a responder label, got responder %s", conv.ResponderMemberID)
	}

	if got := convOpener.candidates[result.ConversationID]; len(got) != len(members) {
		t.Errorf("stored %d candidates, want %d", len(got), len(members))
	}

	opened, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_CONVERSATION_OPENED)
	if !ok {
		t.Fatal("no CONVERSATION_OPENED published")
	}

	// The empty responder id is the wire marker the pool notifier keys on;
	// uuid.Nil.String() would silently break candidate nudges.
	if got := opened.GetConversationOpened().GetMemberId(); got != "" {
		t.Errorf("opened event responder id = %q, want empty for a pool dispatch", got)
	}
}

// TestAsk_ExactlyOneTarget rejects asks providing both or neither of
// member_id and domains, without touching the store.
func TestAsk_ExactlyOneTarget(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		responder uuid.UUID
		domains   []string
	}{
		{name: "both", responder: uuid.New(), domains: []string{"db"}},
		{name: "neither", responder: uuid.Nil, domains: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			convOpener := newFakeConversationOpener()
			h := MustNewAskHandler(convOpener, newFakeConvRepo(), &fakeEventBus{},
				newFakeProviderLookup(&noopProvider{}), &fakeCandidatePool{}, alwaysDashboardMembers{}, newFakeProjectRepo(), fakeTransactor{}, nil)

			_, err := h.Handle(t.Context(), AskCommand{
				Summary:           "test summary",
				Tags:              []string{"topic"},
				ProjectID:         uuid.New(),
				AskerMemberID:     uuid.New(),
				ResponderMemberID: tc.responder,
				Domains:           tc.domains,
				Question:          "q",
			})

			var invalid errs.InvalidError
			if !errors.As(err, &invalid) {
				t.Fatalf("want InvalidError, got %v", err)
			}

			if len(convOpener.conversations) != 0 {
				t.Error("a rejected ask must not open a conversation")
			}
		})
	}
}

// TestAsk_EmptyPoolRejected proves an empty candidate resolution
// rejects the ask before any write — never an orphan conversation.
func TestAsk_EmptyPoolRejected(t *testing.T) {
	t.Parallel()

	convOpener := newFakeConversationOpener()
	h := MustNewAskHandler(convOpener, newFakeConvRepo(), &fakeEventBus{},
		newFakeProviderLookup(&noopProvider{}), &fakeCandidatePool{}, alwaysDashboardMembers{}, newFakeProjectRepo(), fakeTransactor{}, nil)

	_, err := h.Handle(t.Context(), AskCommand{
		Summary:       "test summary",
		Tags:          []string{"topic"},
		ProjectID:     uuid.New(),
		AskerMemberID: uuid.New(),
		Domains:       []string{"nobody-knows-this"},
		Question:      "q",
	})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("want InvalidError, got %v", err)
	}

	if len(convOpener.conversations) != 0 {
		t.Error("an empty pool must not open a conversation")
	}
}

// ---- pool replies (hub-and-spoke phase 2: no claim gate) ---------------------

// seedPoolConversation stores an open, unlabeled pool conversation and returns it.
func seedPoolConversation(repo *fakeConversationRepository) model.Conversation {
	conv := model.Conversation{
		ID:            uuid.New(),
		ProjectID:     uuid.New(),
		AskerMemberID: uuid.New(),
		Status:        model.ConversationStatusOpen,
		Question:      "Which index fits this query?",
	}
	repo.conversations[conv.ID] = conv

	return conv
}

// newFollowUpHandler wires a FollowUpHandler with the standard test fakes.
func newFollowUpHandler(repo *fakeConversationRepository, bus *fakeEventBus, labeler *fakeLabeler, participants participantStore) FollowUpHandler {
	if participants == nil {
		participants = &fakeParticipantStore{}
	}

	return MustNewFollowUpHandler(repo, bus, labeler, participants, fakeTransactor{}, nil)
}

// TestFollowUp_OverwritesProvidedMetadataOnly proves optional follow_up
// metadata overwrites only the facets the agent supplied (normalized) and
// preserves the rest of the conversation's existing metadata.
func TestFollowUp_OverwritesProvidedMetadataOnly(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	conv := seedPoolConversation(repo)

	c := repo.conversations[conv.ID]
	c.Summary = "old summary"
	c.Tags = []string{"old"}
	c.Entities = []string{"old-entity"}
	repo.conversations[conv.ID] = c

	labeler := newFakeLabeler(repo)
	author := uuid.New()
	labeler.markActive(conv.ID, author)

	h := newFollowUpHandler(repo, &fakeEventBus{}, labeler, nil)

	if _, err := h.Handle(t.Context(), FollowUpCommand{
		ConversationID: conv.ID,
		AuthorMemberID: author,
		Message:        "an answer",
		Tags:           []string{"New Tag"},
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got := repo.conversations[conv.ID]
	if got.Summary != "old summary" {
		t.Errorf("summary = %q, want preserved (untouched)", got.Summary)
	}

	if len(got.Tags) != 1 || got.Tags[0] != "new tag" {
		t.Errorf("tags = %v, want normalized [new tag]", got.Tags)
	}

	if len(got.Entities) != 1 || got.Entities[0] != "old-entity" {
		t.Errorf("entities = %v, want preserved (untouched)", got.Entities)
	}
}

// TestFollowUp_NoMetadataLeavesUntouched proves a follow_up with no metadata is
// a no-op on the conversation's existing summary/tags/entities.
func TestFollowUp_NoMetadataLeavesUntouched(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	conv := seedPoolConversation(repo)

	c := repo.conversations[conv.ID]
	c.Summary = "keep me"
	c.Tags = []string{"keep"}
	repo.conversations[conv.ID] = c

	labeler := newFakeLabeler(repo)
	author := uuid.New()
	labeler.markActive(conv.ID, author)

	h := newFollowUpHandler(repo, &fakeEventBus{}, labeler, nil)

	if _, err := h.Handle(t.Context(), FollowUpCommand{
		ConversationID: conv.ID,
		AuthorMemberID: author,
		Message:        "an answer",
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got := repo.conversations[conv.ID]
	if got.Summary != "keep me" || len(got.Tags) != 1 || got.Tags[0] != "keep" {
		t.Errorf("metadata changed: summary=%q tags=%v, want untouched", got.Summary, got.Tags)
	}
}

// TestFollowUp_TwoCandidatesBothAppend is the phase-2 acceptance criterion:
// two candidates replying in any order both append visible messages; neither
// gets an "already claimed" error — the claim model is gone.
func TestFollowUp_TwoCandidatesBothAppend(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	conv := seedPoolConversation(repo)
	labeler := newFakeLabeler(repo)
	bus := &fakeEventBus{}

	first, second := uuid.New(), uuid.New()
	labeler.markActive(conv.ID, first)
	labeler.markActive(conv.ID, second)

	h := newFollowUpHandler(repo, bus, labeler, nil)

	for i, author := range []uuid.UUID{first, second} {
		result, err := h.Handle(t.Context(), FollowUpCommand{
			ConversationID: conv.ID,
			AuthorMemberID: author,
			Message:        "an answer",
		})
		if err != nil {
			t.Fatalf("reply %d: a second responder must never be rejected, got %v", i+1, err)
		}

		if result.Outcome != OutcomeAppended {
			t.Errorf("reply %d: Outcome = %v, want OutcomeAppended", i+1, result.Outcome)
		}
	}

	if got := len(repo.messages[conv.ID]); got != 2 {
		t.Errorf("want both replies persisted, got %d messages", got)
	}

	if got := bus.countOfType(orakov1.EventType_EVENT_TYPE_MESSAGE_POSTED); got != 2 {
		t.Errorf("want MESSAGE_POSTED published for both replies, got %d", got)
	}
}

// TestFollowUp_ExactlyOneSpecialistLabel proves the descriptive label is
// stamped exactly once: the first answerer wins the CAS, and later answers
// never move it.
func TestFollowUp_ExactlyOneSpecialistLabel(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	conv := seedPoolConversation(repo)
	labeler := newFakeLabeler(repo)
	bus := &fakeEventBus{}

	first, second := uuid.New(), uuid.New()
	labeler.markActive(conv.ID, first)
	labeler.markActive(conv.ID, second)

	h := newFollowUpHandler(repo, bus, labeler, nil)

	for _, author := range []uuid.UUID{first, second} {
		if _, err := h.Handle(t.Context(), FollowUpCommand{
			ConversationID: conv.ID,
			AuthorMemberID: author,
			Message:        "an answer",
		}); err != nil {
			t.Fatalf("Handle(%s): %v", author, err)
		}
	}

	if got := repo.conversations[conv.ID].ResponderMemberID; got != first {
		t.Errorf("responder label = %s, want the first answerer %s only", got, first)
	}
}

// TestFollowUp_FirstAnswerStampsLabelAndAnswers proves a candidate's first
// reply stamps the descriptive responder label and flips the conversation
// open → answered, publishing MESSAGE_POSTED with the ANSWER role.
func TestFollowUp_FirstAnswerStampsLabelAndAnswers(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	conv := seedPoolConversation(repo)
	labeler := newFakeLabeler(repo)
	bus := &fakeEventBus{}

	candidate := uuid.New()
	labeler.markActive(conv.ID, candidate)

	h := newFollowUpHandler(repo, bus, labeler, nil)

	if _, err := h.Handle(t.Context(), FollowUpCommand{
		ConversationID: conv.ID,
		AuthorMemberID: candidate,
		Message:        "Use a partial index on status.",
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got := repo.conversations[conv.ID]
	if got.ResponderMemberID != candidate {
		t.Errorf("responder label = %s, want the first answerer %s", got.ResponderMemberID, candidate)
	}

	if got.Status != model.ConversationStatusAnswered {
		t.Errorf("status = %s, want answered", got.Status)
	}

	env, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_MESSAGE_POSTED)
	if !ok {
		t.Fatal("no MESSAGE_POSTED published")
	}

	if role := env.GetMessagePosted().GetRole(); role != orakov1.MessageRole_MESSAGE_ROLE_ANSWER {
		t.Errorf("published role = %v, want MESSAGE_ROLE_ANSWER", role)
	}
}

// TestFollowUp_LostLabelRace_ReplyStillAppends covers the CAS race: the
// reply's read saw an unlabeled conversation but the stamp loses to a
// concurrent one. Losing is a non-event — the reply appends regardless.
func TestFollowUp_LostLabelRace_ReplyStillAppends(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	conv := seedPoolConversation(repo)
	labeler := newFakeLabeler(repo)
	labeler.denyClaim = true

	candidate := uuid.New()
	labeler.markActive(conv.ID, candidate)

	h := newFollowUpHandler(repo, &fakeEventBus{}, labeler, nil)

	result, err := h.Handle(t.Context(), FollowUpCommand{
		ConversationID: conv.ID,
		AuthorMemberID: candidate,
		Message:        "still a valid answer",
	})
	if err != nil {
		t.Fatalf("losing the label race must not reject the reply, got %v", err)
	}

	if result.Outcome != OutcomeAppended {
		t.Errorf("Outcome = %v, want OutcomeAppended", result.Outcome)
	}

	if got := len(repo.messages[conv.ID]); got != 1 {
		t.Errorf("want the reply appended despite the lost CAS, got %d messages", got)
	}
}

// TestFollowUp_LabelerErrorDoesNotFailReply proves a labeler CAS error is a
// non-event too: the reply still appends and the command succeeds — the error
// is only logged. The author is an added participant so the authorization
// path never touches the (broken) labeler write.
func TestFollowUp_LabelerErrorDoesNotFailReply(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	conv := seedPoolConversation(repo)
	labeler := newFakeLabeler(repo)
	labeler.claimErr = errors.New("label store unavailable")

	participant := uuid.New()
	parts := &fakeParticipantStore{existing: []model.ConversationParticipant{{MemberID: participant}}}

	h := newFollowUpHandler(repo, &fakeEventBus{}, labeler, parts)

	result, err := h.Handle(t.Context(), FollowUpCommand{
		ConversationID: conv.ID,
		AuthorMemberID: participant,
		Message:        "answer despite a broken label store",
	})
	if err != nil {
		t.Fatalf("a labeler error must not fail the reply, got %v", err)
	}

	if result.Outcome != OutcomeAppended {
		t.Errorf("Outcome = %v, want OutcomeAppended", result.Outcome)
	}

	if got := len(repo.messages[conv.ID]); got != 1 {
		t.Errorf("want the reply appended despite the labeler error, got %d messages", got)
	}

	if got := repo.conversations[conv.ID].ResponderMemberID; got != uuid.Nil {
		t.Errorf("responder label = %s, must stay unset when the CAS errored", got)
	}
}

// TestFollowUp_NonParticipantForbidden keeps the conversation closed to
// strangers: not the asker, not the responder, never added, never a
// candidate — the only remaining reply gate.
func TestFollowUp_NonParticipantForbidden(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	conv := seedPoolConversation(repo)
	bus := &fakeEventBus{}

	h := newFollowUpHandler(repo, bus, newFakeLabeler(repo), nil)

	_, err := h.Handle(t.Context(), FollowUpCommand{
		ConversationID: conv.ID,
		AuthorMemberID: uuid.New(),
		Message:        "let me in",
	})

	var forbidden errs.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("want ForbiddenError, got %v", err)
	}

	if got := len(repo.messages[conv.ID]); got != 0 {
		t.Errorf("a forbidden reply must not be appended, got %d messages", got)
	}

	if got := bus.countOfType(orakov1.EventType_EVENT_TYPE_MESSAGE_POSTED); got != 0 {
		t.Errorf("a forbidden reply must not publish MESSAGE_POSTED, got %d", got)
	}
}

// TestFollowUp_AskerFollowUpReopensAnswered proves the dialogue state machine:
// the asker's follow-up on an answered conversation brings it back to open.
func TestFollowUp_AskerFollowUpReopensAnswered(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	conv := seedPoolConversation(repo)
	conv.Status = model.ConversationStatusAnswered
	repo.conversations[conv.ID] = conv

	h := newFollowUpHandler(repo, &fakeEventBus{}, newFakeLabeler(repo), nil)

	if _, err := h.Handle(t.Context(), FollowUpCommand{
		ConversationID: conv.ID,
		AuthorMemberID: conv.AskerMemberID,
		Message:        "Any update?",
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if got := repo.conversations[conv.ID].Status; got != model.ConversationStatusOpen {
		t.Errorf("status = %s, want open after an asker follow-up", got)
	}
}

// TestFollowUp_ClosedConversationRejected proves a closed thread is immutable.
func TestFollowUp_ClosedConversationRejected(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	conv := seedPoolConversation(repo)
	conv.Status = model.ConversationStatusResolved
	repo.conversations[conv.ID] = conv

	h := newFollowUpHandler(repo, &fakeEventBus{}, newFakeLabeler(repo), nil)

	_, err := h.Handle(t.Context(), FollowUpCommand{
		ConversationID: conv.ID,
		AuthorMemberID: conv.AskerMemberID,
		Message:        "too late",
	})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("want InvalidError on a closed conversation, got %v", err)
	}
}
