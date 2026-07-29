// SPDX-License-Identifier: AGPL-3.0-or-later

package command_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// fakeKnowledgeCreator records the entry it was asked to create.
type fakeKnowledgeCreator struct {
	got   model.KnowledgeEntry
	calls int
}

func (f *fakeKnowledgeCreator) CreateEntry(_ context.Context, e model.KnowledgeEntry) (model.KnowledgeEntry, error) {
	f.got = e
	f.calls++

	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}

	return e, nil
}

// fakeConvPromoteReader serves a fixed conversation + messages.
type fakeConvPromoteReader struct {
	conv model.Conversation
	msgs []model.Message
	err  error
}

func (f *fakeConvPromoteReader) ConversationByID(_ context.Context, _ uuid.UUID) (model.Conversation, error) {
	if f.err != nil {
		return model.Conversation{}, f.err
	}

	return f.conv, nil
}

func (f *fakeConvPromoteReader) MessagesByConversation(_ context.Context, _ uuid.UUID) ([]model.Message, error) {
	return f.msgs, nil
}

// fakeKnowledgeLifecycle backs the approve/dismiss/revalidate handlers.
type fakeKnowledgeLifecycle struct {
	entry      model.KnowledgeEntry
	entryErr   error
	activated  int
	deleted    int
	activateEr error
	deleteErr  error
}

func (f *fakeKnowledgeLifecycle) EntryByID(_ context.Context, _ uuid.UUID) (model.KnowledgeEntry, error) {
	return f.entry, f.entryErr
}

func (f *fakeKnowledgeLifecycle) Activate(_ context.Context, _ uuid.UUID) error {
	f.activated++

	return f.activateEr
}

func (f *fakeKnowledgeLifecycle) DeleteEntry(_ context.Context, _ uuid.UUID) error {
	f.deleted++

	return f.deleteErr
}

// TestPromoteConversationComposesCuratedEntry proves a resolved conversation is
// distilled into a curated, active entry carrying its question, the first
// responder's composed answer, and its summary/tags/entities.
func TestPromoteConversationComposesCuratedEntry(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	projectID := uuid.New()
	promoter := uuid.New()
	responder := uuid.New()
	asker := uuid.New()

	reader := &fakeConvPromoteReader{
		conv: model.Conversation{
			ID:                uuid.New(),
			ProjectID:         projectID,
			AskerMemberID:     asker,
			ResponderMemberID: responder,
			Status:            model.ConversationStatusResolved,
			Question:          "How do I rotate the Discord token?",
			Summary:           "discord token rotation",
			Tags:              []string{"discord", "config"},
			Entities:          []string{"bot-token"},
		},
		msgs: []model.Message{
			{AuthorMemberID: asker, Role: model.MessageRoleQuestion, Body: "how?"},
			{AuthorMemberID: responder, Role: model.MessageRoleAnswer, Body: "Set ORAKO_DISCORD_TOKEN."},
			{AuthorMemberID: responder, Role: model.MessageRoleAnswer, Body: "Then restart the server."},
			{AuthorMemberID: uuid.New(), Role: model.MessageRoleSecondOpinion, Body: "ignore me"},
		},
		err: nil,
	}
	store := &fakeKnowledgeCreator{}

	h := command.MustNewPromoteConversationToKnowledgeHandler(reader, store)

	res, err := h.Handle(context.Background(), command.PromoteConversationToKnowledgeCommand{
		ConversationID:   reader.conv.ID,
		OrgID:            orgID,
		PromoterMemberID: promoter,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if res.EntryID == uuid.Nil {
		t.Error("expected a non-nil entry id")
	}

	got := store.got
	if got.Source != model.KnowledgeSourceCurated || got.Status != model.KnowledgeStatusActive {
		t.Errorf("promoted entry must be curated+active: source=%q status=%q", got.Source, got.Status)
	}

	if got.OrgID != orgID || got.ProjectID != projectID || got.AuthorMemberID != promoter {
		t.Errorf("scope/author mismatch: %+v", got)
	}

	if got.Question != "How do I rotate the Discord token?" {
		t.Errorf("question not carried: %q", got.Question)
	}

	// The two answer messages from the first responder are joined; the second
	// opinion from another member is excluded.
	wantAnswer := "Set ORAKO_DISCORD_TOKEN.\n\nThen restart the server."
	if got.Answer != wantAnswer {
		t.Errorf("answer = %q, want %q", got.Answer, wantAnswer)
	}

	if len(got.Tags) != 2 || got.Tags[0] != "discord" {
		t.Errorf("tags not carried: %v", got.Tags)
	}
}

// TestPromoteRejectsUnresolved proves only a resolved conversation may be
// promoted.
func TestPromoteRejectsUnresolved(t *testing.T) {
	t.Parallel()

	reader := &fakeConvPromoteReader{
		conv: model.Conversation{
			ID:            uuid.New(),
			ProjectID:     uuid.New(),
			AskerMemberID: uuid.New(),
			Status:        model.ConversationStatusOpen,
			Question:      "still open",
		},
		msgs: nil,
		err:  nil,
	}
	store := &fakeKnowledgeCreator{}

	h := command.MustNewPromoteConversationToKnowledgeHandler(reader, store)

	_, err := h.Handle(context.Background(), command.PromoteConversationToKnowledgeCommand{
		ConversationID: reader.conv.ID,
		OrgID:          uuid.New(),
	})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected InvalidError for an unresolved conversation, got %v", err)
	}

	if store.calls != 0 {
		t.Error("no entry should be created for an unresolved conversation")
	}
}

// TestApproveAndRevalidateActivate proves both approve and revalidate flip the
// entry to active via the store, after the org-ownership check.
func TestApproveAndRevalidateActivate(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	entry := model.KnowledgeEntry{
		ID:     uuid.New(),
		OrgID:  orgID,
		Status: model.KnowledgeStatusPending,
		Source: model.KnowledgeSourceAgentSuggested,
	}

	store := &fakeKnowledgeLifecycle{entry: entry}

	approve := command.MustNewApproveKnowledgeEntryHandler(store)
	if _, err := approve.Handle(context.Background(), command.ApproveKnowledgeEntryCommand{EntryID: entry.ID, OrgID: orgID}); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	reval := command.MustNewRevalidateKnowledgeEntryHandler(store)
	if _, err := reval.Handle(context.Background(), command.RevalidateKnowledgeEntryCommand{EntryID: entry.ID, OrgID: orgID}); err != nil {
		t.Fatalf("Revalidate: %v", err)
	}

	if store.activated != 2 {
		t.Errorf("expected two Activate calls (approve + revalidate), got %d", store.activated)
	}
}

// TestApproveCrossOrgNotFound proves the org-ownership guard: an entry in another
// org is reported not-found (no existence leak) and never activated.
func TestApproveCrossOrgNotFound(t *testing.T) {
	t.Parallel()

	entry := model.KnowledgeEntry{
		ID:     uuid.New(),
		OrgID:  uuid.New(), // a DIFFERENT org
		Status: model.KnowledgeStatusPending,
	}
	store := &fakeKnowledgeLifecycle{entry: entry}

	approve := command.MustNewApproveKnowledgeEntryHandler(store)

	_, err := approve.Handle(context.Background(), command.ApproveKnowledgeEntryCommand{EntryID: entry.ID, OrgID: uuid.New()})

	var nf errs.NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("cross-org approve should be not-found, got %v", err)
	}

	if store.activated != 0 {
		t.Error("a cross-org entry must never be activated")
	}
}

// TestDismissOnlyPending proves dismiss deletes a pending entry but refuses a
// published (active) one.
func TestDismissOnlyPending(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()

	pending := model.KnowledgeEntry{
		ID:     uuid.New(),
		OrgID:  orgID,
		Status: model.KnowledgeStatusPending,
	}
	pendingStore := &fakeKnowledgeLifecycle{entry: pending}

	dismiss := command.MustNewDismissKnowledgeEntryHandler(pendingStore)
	if _, err := dismiss.Handle(context.Background(), command.DismissKnowledgeEntryCommand{EntryID: pending.ID, OrgID: orgID}); err != nil {
		t.Fatalf("Dismiss(pending): %v", err)
	}

	if pendingStore.deleted != 1 {
		t.Errorf("a pending entry must be deleted, deleted=%d", pendingStore.deleted)
	}

	active := model.KnowledgeEntry{
		ID:     uuid.New(),
		OrgID:  orgID,
		Status: model.KnowledgeStatusActive,
	}
	activeStore := &fakeKnowledgeLifecycle{entry: active}

	dismiss = command.MustNewDismissKnowledgeEntryHandler(activeStore)

	_, err := dismiss.Handle(context.Background(), command.DismissKnowledgeEntryCommand{EntryID: active.ID, OrgID: orgID})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("dismissing an active entry should be rejected, got %v", err)
	}

	if activeStore.deleted != 0 {
		t.Error("an active entry must never be deleted via dismiss")
	}
}

// TestSuggestCreatesPending proves the suggest handler builds an
// agent-suggested, pending entry.
func TestSuggestCreatesPending(t *testing.T) {
	t.Parallel()

	store := &fakeKnowledgeCreator{}
	h := command.MustNewSuggestKnowledgeEntryHandler(store)

	_, err := h.Handle(context.Background(), command.SuggestKnowledgeEntryCommand{
		OrgID:          uuid.New(),
		AuthorMemberID: uuid.New(),
		Question:       "durable thing?",
		Answer:         "the fix",
		Tags:           []string{"ops"},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if store.got.Source != model.KnowledgeSourceAgentSuggested || store.got.Status != model.KnowledgeStatusPending {
		t.Errorf("suggestion must be agent_suggested+pending: source=%q status=%q", store.got.Source, store.got.Status)
	}
}
