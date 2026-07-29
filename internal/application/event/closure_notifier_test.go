// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"strings"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
)

func closedMsg(t *testing.T, closerID, targetID uuid.UUID, resolution string) *message.Message {
	t.Helper()

	env := &orakov1.Envelope{
		ProjectId: uuid.NewString(),
		Type:      orakov1.EventType_EVENT_TYPE_CONVERSATION_CLOSED,
		Payload: &orakov1.Envelope_ConversationClosed{
			ConversationClosed: &orakov1.ConversationClosed{
				ConversationId:    uuid.NewString(),
				Resolution:        resolution,
				KbEntryId:         uuid.NewString(),
				CloserMemberId:    closerID.String(),
				ResponderMemberId: targetID.String(),
			},
		},
	}

	payload, err := proto.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}

	return message.NewMessage(uuid.NewString(), payload)
}

// TestClosureNotifier_AgentCloseNotifiesSpecialist is the feedback-loop
// guarantee: the operator learns what the agent distilled in their name.
func TestClosureNotifier_AgentCloseNotifiesSpecialist(t *testing.T) {
	t.Parallel()

	mailer := &fakeMailer{}
	responder := model.Member{ID: uuid.New(), DisplayName: "Sam", Email: "sam@example.com"}
	members := &fakeMemberReader{member: responder}

	handler := ClosureNotifier(members, mailer, "http://localhost:5173", quietLogger())

	if err := handler(closedMsg(t, uuid.New(), responder.ID, "Plausible first; PostHog later.")); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(mailer.sent) != 1 {
		t.Fatalf("sent %d emails, want 1", len(mailer.sent))
	}

	body := mailer.sent[0].TextBody
	if !strings.Contains(body, "Plausible first; PostHog later.") || !strings.Contains(body, "/conversations/") {
		t.Errorf("closure email missing resolution or link: %s", body)
	}
}

// TestClosureNotifier_SelfCloseStaysSilent: a responder who wrote and closed
// their own resolution needs no echo.
func TestClosureNotifier_SelfCloseStaysSilent(t *testing.T) {
	t.Parallel()

	mailer := &fakeMailer{}
	targetID := uuid.New()
	members := &fakeMemberReader{member: model.Member{ID: targetID, Email: "sam@example.com"}}

	handler := ClosureNotifier(members, mailer, "http://localhost:5173", quietLogger())

	if err := handler(closedMsg(t, targetID, targetID, "self-written")); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(mailer.sent) != 0 {
		t.Errorf("self-close must not email, sent %d", len(mailer.sent))
	}
}

// TestClosureNotifier_NoResolutionStaysSilent: closing without an answer
// promoted nothing — no notification.
func TestClosureNotifier_NoResolutionStaysSilent(t *testing.T) {
	t.Parallel()

	mailer := &fakeMailer{}
	members := &fakeMemberReader{member: model.Member{ID: uuid.New(), Email: "sam@example.com"}}

	handler := ClosureNotifier(members, mailer, "http://localhost:5173", quietLogger())

	if err := handler(closedMsg(t, uuid.New(), uuid.New(), "")); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(mailer.sent) != 0 {
		t.Errorf("empty resolution must not email, sent %d", len(mailer.sent))
	}
}
