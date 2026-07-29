// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

type fakeInboxReader struct {
	convs    []model.Conversation
	orgConvs []model.Conversation
	err      error
}

func (f *fakeInboxReader) InboxByMember(_ context.Context, _ uuid.UUID) ([]ConversationRecord, error) {
	return convsToRecords(f.convs), f.err
}

func (f *fakeInboxReader) OpenConversationsByOrg(_ context.Context, _ uuid.UUID) ([]ConversationRecord, error) {
	return convsToRecords(f.orgConvs), f.err
}

func TestListInbox_MapsConversationsToItems(t *testing.T) {
	t.Parallel()

	responder := uuid.New()
	asker := uuid.New()
	project := uuid.New()
	convID := uuid.New()

	reader := &fakeInboxReader{convs: []model.Conversation{{
		ID:                convID,
		ProjectID:         project,
		AskerMemberID:     asker,
		ResponderMemberID: responder,
		Status:            model.ConversationStatusOpen,
		Question:          "How do we rotate refresh tokens?",
		CreatedAt:         time.Now().UTC(),
	}}}

	items, err := MustNewListInboxHandler(reader, newFakeCandidateReader()).Handle(context.Background(), ListInboxQuery{ResponderMemberID: responder})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}

	it := items[0]
	if it.ConversationID != convID || it.ProjectID != project || it.AskerMemberID != asker {
		t.Errorf("item ids not mapped correctly: %+v", it)
	}

	if it.Question != "How do we rotate refresh tokens?" {
		t.Errorf("question = %q", it.Question)
	}
}

// TestListInbox_OrgAdminBypass proves an org admin (with a resolved org) receives
// the org-wide open set, not the responder-scoped inbox.
func TestListInbox_OrgAdminBypass(t *testing.T) {
	t.Parallel()

	admin := uuid.New()
	orgID := uuid.New()

	scoped := model.Conversation{ID: uuid.New(), ResponderMemberID: admin, Status: model.ConversationStatusOpen, Question: "addressed to admin"}
	// An unanswered pool conversation surfaces via the pool reader AND the
	// org-wide open set; the bypass must skip it there so it is listed once.
	pool := model.Conversation{ID: uuid.New(), Status: model.ConversationStatusOpen, Question: "awaiting anyone's reply"}
	orgWide := []model.Conversation{
		{ID: uuid.New(), ResponderMemberID: uuid.New(), Status: model.ConversationStatusOpen, Question: "org q1"},
		{ID: uuid.New(), ResponderMemberID: uuid.New(), Status: model.ConversationStatusOpen, Question: "org q2"},
		pool,
	}

	reader := &fakeInboxReader{convs: []model.Conversation{scoped}, orgConvs: orgWide}
	candidates := newFakeCandidateReader()
	candidates.pool = []model.Conversation{pool}

	items, err := MustNewListInboxHandler(reader, candidates).Handle(context.Background(), ListInboxQuery{
		ResponderMemberID: admin,
		OrgID:             orgID,
		IsOrgAdmin:        true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(items) != 3 {
		t.Fatalf("org admin must see pool + org-wide set without dupes (3), got %d", len(items))
	}

	if items[0].ConversationID != pool.ID {
		t.Errorf("first item must be the pool conversation, got %+v", items[0])
	}
}

// TestListInbox_OrgAdminWithoutOrgFallsBackToSpecialist proves the bypass is
// inert without a resolved org, so a responder scope is still applied.
func TestListInbox_OrgAdminWithoutOrgFallsBackToSpecialist(t *testing.T) {
	t.Parallel()

	scoped := model.Conversation{ID: uuid.New(), ResponderMemberID: uuid.New(), Status: model.ConversationStatusOpen, Question: "scoped"}
	reader := &fakeInboxReader{convs: []model.Conversation{scoped}, orgConvs: []model.Conversation{{ID: uuid.New()}}}

	items, err := MustNewListInboxHandler(reader, newFakeCandidateReader()).Handle(context.Background(), ListInboxQuery{
		ResponderMemberID: uuid.New(),
		OrgID:             uuid.Nil,
		IsOrgAdmin:        true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("want the responder-scoped inbox (1) when no org resolved, got %d", len(items))
	}
}

// TestListInbox_PoolItemsFirst proves unanswered pool conversations the caller
// was contacted on lead the inbox, ahead of the caller's owned open
// conversations.
func TestListInbox_PoolItemsFirst(t *testing.T) {
	t.Parallel()

	responder := uuid.New()
	owned := model.Conversation{ID: uuid.New(), ResponderMemberID: responder, Status: model.ConversationStatusOpen, Question: "mine"}
	pool := model.Conversation{ID: uuid.New(), Status: model.ConversationStatusOpen, Question: "awaiting anyone's reply"}

	reader := &fakeInboxReader{convs: []model.Conversation{owned}}
	candidates := newFakeCandidateReader()
	candidates.pool = []model.Conversation{pool}

	items, err := MustNewListInboxHandler(reader, candidates).Handle(context.Background(), ListInboxQuery{ResponderMemberID: responder})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("want pool + owned = 2 items, got %d", len(items))
	}

	if items[0].ConversationID != pool.ID {
		t.Errorf("first item must be the pool conversation, got %+v", items[0])
	}

	if items[1].ConversationID != owned.ID {
		t.Errorf("second item must be the owned conversation, got %+v", items[1])
	}
}

func TestListInbox_EmptyIsNonNilSlice(t *testing.T) {
	t.Parallel()

	items, err := MustNewListInboxHandler(&fakeInboxReader{}, newFakeCandidateReader()).Handle(context.Background(), ListInboxQuery{ResponderMemberID: uuid.New()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if items == nil {
		t.Error("want non-nil empty slice")
	}
}

func TestListInbox_PropagatesReaderError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("db down")

	_, err := MustNewListInboxHandler(&fakeInboxReader{err: sentinel}, newFakeCandidateReader()).Handle(context.Background(), ListInboxQuery{ResponderMemberID: uuid.New()})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel error, got %v", err)
	}
}
