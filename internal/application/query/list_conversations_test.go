// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

func TestListConversations_AllStatuses(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	specID := uuid.New()

	c1 := model.Conversation{
		ID:                uuid.New(),
		ProjectID:         projectID,
		ResponderMemberID: specID,
		Status:            model.ConversationStatusOpen,
		Question:          "first",
		CreatedAt:         time.Now().Add(-2 * time.Minute),
	}
	c2 := model.Conversation{
		ID:                uuid.New(),
		ProjectID:         projectID,
		ResponderMemberID: specID,
		Status:            model.ConversationStatusAnswered,
		Question:          "second",
		CreatedAt:         time.Now().Add(-1 * time.Minute),
	}

	reader := &fakeConversationsByProjectReader{conversations: []model.Conversation{c1, c2}}
	h := MustNewListConversationsHandler(reader, &fakeParticipantsNames{}, &fakeParticipantsNames{})

	summaries, err := h.Handle(t.Context(), ListConversationsQuery{
		ProjectIDs: []uuid.UUID{projectID},
		Status:     "",
		IsOrgAdmin: true, // admin sees all; this case exercises the status filter
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(summaries) != 2 {
		t.Fatalf("got %d summaries, want 2", len(summaries))
	}
}

func TestListConversations_StatusFilter(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()

	open := model.Conversation{
		ID:        uuid.New(),
		ProjectID: projectID,
		Status:    model.ConversationStatusOpen,
		Question:  "open q",
		CreatedAt: time.Now(),
	}
	closed := model.Conversation{
		ID:        uuid.New(),
		ProjectID: projectID,
		Status:    model.ConversationStatusResolved,
		Question:  "closed q",
		CreatedAt: time.Now(),
	}

	reader := &fakeConversationsByProjectReader{conversations: []model.Conversation{open, closed}}
	h := MustNewListConversationsHandler(reader, &fakeParticipantsNames{}, &fakeParticipantsNames{})

	summaries, err := h.Handle(t.Context(), ListConversationsQuery{
		ProjectIDs: []uuid.UUID{projectID},
		Status:     "open",
		IsOrgAdmin: true,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(summaries) != 1 {
		t.Fatalf("got %d summaries, want 1 (closed filtered out)", len(summaries))
	}

	if summaries[0].Status != model.ConversationStatusOpen {
		t.Errorf("status = %q, want open", summaries[0].Status)
	}
}

func TestListConversations_DifferentProject_Excluded(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	otherProjectID := uuid.New()

	mine := model.Conversation{
		ID:        uuid.New(),
		ProjectID: projectID,
		Status:    model.ConversationStatusOpen,
		Question:  "mine",
		CreatedAt: time.Now(),
	}
	theirs := model.Conversation{
		ID:        uuid.New(),
		ProjectID: otherProjectID,
		Status:    model.ConversationStatusOpen,
		Question:  "theirs",
		CreatedAt: time.Now(),
	}

	reader := &fakeConversationsByProjectReader{conversations: []model.Conversation{mine, theirs}}
	h := MustNewListConversationsHandler(reader, &fakeParticipantsNames{}, &fakeParticipantsNames{})

	summaries, err := h.Handle(t.Context(), ListConversationsQuery{
		ProjectIDs: []uuid.UUID{projectID},
		Status:     "",
		IsOrgAdmin: true,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(summaries) != 1 {
		t.Fatalf("got %d summaries, want 1 (other project excluded)", len(summaries))
	}

	if summaries[0].Question != "mine" {
		t.Errorf("question = %q, want mine", summaries[0].Question)
	}
}

// TestListConversations_Visibility proves the owner-or-assigned-responder scope
// and the org-admin bypass: a member sees only conversations they asked or are
// the assigned responder for; an org admin sees them all.
func TestListConversations_Visibility(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	asker := uuid.New()
	responder := uuid.New()
	other := uuid.New()

	mkConv := func(a, s uuid.UUID, q string) model.Conversation {
		return model.Conversation{
			ID:                uuid.New(),
			ProjectID:         projectID,
			AskerMemberID:     a,
			ResponderMemberID: s,
			Status:            model.ConversationStatusOpen,
			Question:          q,
			CreatedAt:         time.Now(),
		}
	}

	// asked: owned by `asker`; assigned: answered by `responder`; foreign: neither.
	asked := mkConv(asker, uuid.New(), "asked-by-asker")
	assigned := mkConv(uuid.New(), responder, "assigned-to-responder")
	foreign := mkConv(uuid.New(), uuid.New(), "unrelated")

	reader := &fakeConversationsByProjectReader{conversations: []model.Conversation{asked, assigned, foreign}}
	h := MustNewListConversationsHandler(reader, &fakeParticipantsNames{}, &fakeParticipantsNames{})

	cases := []struct {
		name     string
		caller   uuid.UUID
		admin    bool
		wantSet  map[string]bool
		wantSize int
	}{
		{"asker sees only own", asker, false, map[string]bool{"asked-by-asker": true}, 1},
		{"responder sees only assigned", responder, false, map[string]bool{"assigned-to-responder": true}, 1},
		{"unrelated member sees none", other, false, map[string]bool{}, 0},
		{"org admin sees all", other, true, map[string]bool{"asked-by-asker": true, "assigned-to-responder": true, "unrelated": true}, 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := h.Handle(t.Context(), ListConversationsQuery{
				ProjectIDs:     []uuid.UUID{projectID},
				CallerMemberID: tc.caller,
				IsOrgAdmin:     tc.admin,
			})
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}

			if len(got) != tc.wantSize {
				t.Fatalf("got %d conversations, want %d", len(got), tc.wantSize)
			}

			for _, c := range got {
				if !tc.wantSet[c.Question] {
					t.Errorf("caller must not see %q", c.Question)
				}
			}
		})
	}
}
