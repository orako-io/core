// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// TestGetConversation_CandidateVisibility proves an active candidate may read
// an unclaimed pool conversation, an outsider may not, and a claim closes the
// door on the remaining candidates.
func TestGetConversation_CandidateVisibility(t *testing.T) {
	t.Parallel()

	reader := newFakeConversationReader()
	candidates := newFakeCandidateReader()
	h := MustNewGetConversationHandler(reader, candidates, &fakeParticipantsNames{}, &fakeParticipantsNames{}, nil, nil)

	convID := uuid.New()
	candidate := uuid.New()

	conv, _ := model.NewConversation(convID, uuid.New(), uuid.New(), "pool question")
	reader.conversations[convID] = conv
	candidates.active[convID] = map[uuid.UUID]bool{candidate: true}

	if _, err := h.Handle(t.Context(), GetConversationQuery{ConversationID: convID, CallerMemberID: candidate}); err != nil {
		t.Fatalf("active candidate must see the unclaimed conversation: %v", err)
	}

	var forbidden errs.ForbiddenError

	_, err := h.Handle(t.Context(), GetConversationQuery{ConversationID: convID, CallerMemberID: uuid.New()})
	if !errors.As(err, &forbidden) {
		t.Fatalf("outsider must be forbidden, got %v", err)
	}

	// Someone else claims: the candidate's window closes.
	conv.ResponderMemberID = uuid.New()
	reader.conversations[convID] = conv

	_, err = h.Handle(t.Context(), GetConversationQuery{ConversationID: convID, CallerMemberID: candidate})
	if !errors.As(err, &forbidden) {
		t.Fatalf("candidate must lose access once claimed by another, got %v", err)
	}
}

func TestGetConversation_HappyPath(t *testing.T) {
	t.Parallel()

	reader := newFakeConversationReader()
	h := MustNewGetConversationHandler(reader, newFakeCandidateReader(), &fakeParticipantsNames{}, &fakeParticipantsNames{}, nil, nil)

	projectID := uuid.New()
	askerID := uuid.New()
	specID := uuid.New()
	convID := uuid.New()

	conv, _ := model.NewConversation(convID, projectID, askerID, "What is a KB entry?")
	conv.ResponderMemberID = specID
	reader.conversations[convID] = conv

	msgID := uuid.New()
	msg, _ := model.NewMessage(msgID, convID, askerID, model.MessageRoleQuestion, "What is a KB entry?", model.MessageSourceAgent)
	reader.messages[convID] = []model.Message{msg}

	view, err := h.Handle(t.Context(), GetConversationQuery{ConversationID: convID, CallerMemberID: askerID})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if view.ID != convID {
		t.Errorf("view.ID = %v, want %v", view.ID, convID)
	}

	if view.Status != model.ConversationStatusOpen {
		t.Errorf("view.Status = %q, want open", view.Status)
	}

	if len(view.Messages) != 1 {
		t.Fatalf("want 1 message, got %d", len(view.Messages))
	}

	if view.Messages[0].Body != "What is a KB entry?" {
		t.Errorf("message body = %q, want question body", view.Messages[0].Body)
	}
}

func TestGetConversation_NotFound(t *testing.T) {
	t.Parallel()

	h := MustNewGetConversationHandler(newFakeConversationReader(), newFakeCandidateReader(), &fakeParticipantsNames{}, &fakeParticipantsNames{}, nil, nil)

	_, err := h.Handle(t.Context(), GetConversationQuery{ConversationID: uuid.New()})
	if err == nil {
		t.Fatal("expected error for missing conversation")
	}
}

// TestGetConversation_Visibility proves the new authorization check: the asker,
// the assigned responder, and an org admin are allowed; an unrelated member and
// a nil caller are denied with a ForbiddenError.
func TestGetConversation_Visibility(t *testing.T) {
	t.Parallel()

	askerID := uuid.New()
	specID := uuid.New()
	convID := uuid.New()
	projectID := uuid.New()
	otherProject := uuid.New()

	reader := newFakeConversationReader()
	conv, _ := model.NewConversation(convID, projectID, askerID, "q?")
	conv.ResponderMemberID = specID
	reader.conversations[convID] = conv
	reader.messages[convID] = []model.Message{}

	h := MustNewGetConversationHandler(reader, newFakeCandidateReader(), &fakeParticipantsNames{}, &fakeParticipantsNames{}, nil, nil)

	cases := []struct {
		name      string
		caller    uuid.UUID
		admin     bool
		projects  []uuid.UUID
		wantAllow bool
	}{
		{"asker allowed", askerID, false, nil, true},
		{"assigned responder allowed", specID, false, nil, true},
		{"org admin of the conv's project allowed", uuid.New(), true, []uuid.UUID{projectID}, true},
		{"org admin of ANOTHER project denied (H1 cross-tenant)", uuid.New(), true, []uuid.UUID{otherProject}, false},
		{"org admin with no projects denied", uuid.New(), true, nil, false},
		{"unrelated member denied", uuid.New(), false, nil, false},
		{"nil caller denied", uuid.Nil, false, nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := h.Handle(t.Context(), GetConversationQuery{
				ConversationID:   convID,
				CallerMemberID:   tc.caller,
				IsOrgAdmin:       tc.admin,
				CallerProjectIDs: tc.projects,
			})

			switch {
			case tc.wantAllow && err != nil:
				t.Fatalf("want allow, got error %v", err)
			case !tc.wantAllow && err == nil:
				t.Fatal("want ForbiddenError, got nil")
			case !tc.wantAllow:
				var forb errs.ForbiddenError
				if !errors.As(err, &forb) {
					t.Fatalf("want ForbiddenError, got %T: %v", err, err)
				}
			}
		})
	}
}

// Note: ListExperts behavior (every project member is routable; domains are
// surfaced; removed/purged excluded) is covered in list_experts_test.go.
