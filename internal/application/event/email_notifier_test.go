// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"context"
	"log/slog"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
)

type fakeMailer struct {
	sent []model.EmailMessage
	err  error
}

func (f *fakeMailer) Send(_ context.Context, msg model.EmailMessage) error {
	if f.err != nil {
		return f.err
	}

	f.sent = append(f.sent, msg)

	return nil
}

type fakeMemberReader struct {
	member model.Member
	err    error
}

func (f *fakeMemberReader) ByID(_ context.Context, _ uuid.UUID) (model.Member, error) {
	return f.member, f.err
}

func openedMsg(t *testing.T, targetID uuid.UUID) *message.Message {
	t.Helper()

	// A pool dispatch (uuid.Nil) carries an EMPTY responder id on the wire.
	specialistWire := ""
	if targetID != uuid.Nil {
		specialistWire = targetID.String()
	}

	env := &orakov1.Envelope{
		ProjectId: uuid.NewString(),
		Type:      orakov1.EventType_EVENT_TYPE_CONVERSATION_OPENED,
		Payload: &orakov1.Envelope_ConversationOpened{
			ConversationOpened: &orakov1.ConversationOpened{
				ConversationId: uuid.NewString(),
				ProjectId:      uuid.NewString(),
				AskerMemberId:  uuid.NewString(),
				MemberId:       specialistWire,
				Question:       "How do we rotate refresh tokens?",
			},
		},
	}

	payload, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	return message.NewMessage(uuid.NewString(), payload)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// TestEmailNotifier_DashboardMemberGetsEmailed is the core guarantee: a
// dashboard-channel responder with an address receives the notification.
func TestEmailNotifier_DashboardMemberGetsEmailed(t *testing.T) {
	t.Parallel()

	mailer := &fakeMailer{}
	members := &fakeMemberReader{member: model.Member{
		DisplayName:     "Sarah",
		Email:           "sarah@example.com",
		DeliveryChannel: model.DeliveryChannelDashboard,
	}}

	h := EmailNotifier(members, mailer, "https://orako.example.com", quietLogger())
	if err := h(openedMsg(t, uuid.New())); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if len(mailer.sent) != 1 {
		t.Fatalf("want 1 email sent, got %d", len(mailer.sent))
	}

	if mailer.sent[0].To != "sarah@example.com" {
		t.Errorf("To = %q, want sarah@example.com", mailer.sent[0].To)
	}
}

// TestEmailNotifier_ExternalChannelNotEmailed verifies external-channel members
// are NOT emailed (they are already pinged in their app), so we don't double-notify.
func TestEmailNotifier_ExternalChannelNotEmailed(t *testing.T) {
	t.Parallel()

	mailer := &fakeMailer{}
	members := &fakeMemberReader{member: model.Member{
		Email:           "dev@example.com",
		DeliveryChannel: model.DeliveryChannelSlack,
	}}

	h := EmailNotifier(members, mailer, "https://orako.example.com", quietLogger())
	if err := h(openedMsg(t, uuid.New())); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if len(mailer.sent) != 0 {
		t.Fatalf("want 0 emails for a slack-channel member, got %d", len(mailer.sent))
	}
}

// TestEmailNotifier_NoEmailAddressSkipped verifies a dashboard member without an
// address (e.g. after a PII purge) is skipped rather than erroring.
func TestEmailNotifier_NoEmailAddressSkipped(t *testing.T) {
	t.Parallel()

	mailer := &fakeMailer{}
	members := &fakeMemberReader{member: model.Member{DeliveryChannel: model.DeliveryChannelDashboard}}

	h := EmailNotifier(members, mailer, "https://orako.example.com", quietLogger())
	if err := h(openedMsg(t, uuid.New())); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if len(mailer.sent) != 0 {
		t.Fatalf("want 0 emails when address is empty, got %d", len(mailer.sent))
	}
}

// TestEmailNotifier_PoolDispatchIgnored verifies a pool dispatch (empty
// responder id) is NOT handled here: DeliveryNotifier owns the pool fan-out
// exclusively (see delivery_notifier_test.go), so a pool candidate is never
// emailed twice.
func TestEmailNotifier_PoolDispatchIgnored(t *testing.T) {
	t.Parallel()

	mailer := &fakeMailer{}
	members := &fakeMemberReader{member: model.Member{
		DisplayName:     "Sarah",
		Email:           "sarah@example.com",
		DeliveryChannel: model.DeliveryChannelDashboard,
	}}

	h := EmailNotifier(members, mailer, "https://orako.example.com", quietLogger())
	if err := h(openedMsg(t, uuid.Nil)); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if len(mailer.sent) != 0 {
		t.Fatalf("want 0 emails for a pool dispatch (delivery notifier's job), got %d", len(mailer.sent))
	}
}

// TestEmailNotifier_IgnoresOtherEventTypes verifies non-conversation-opened
// events are ignored.
func TestEmailNotifier_IgnoresOtherEventTypes(t *testing.T) {
	t.Parallel()

	mailer := &fakeMailer{}
	members := &fakeMemberReader{member: model.Member{
		Email:           "sarah@example.com",
		DeliveryChannel: model.DeliveryChannelDashboard,
	}}

	env := &orakov1.Envelope{
		ProjectId: uuid.NewString(),
		Type:      orakov1.EventType_EVENT_TYPE_MESSAGE_POSTED,
		Payload: &orakov1.Envelope_MessagePosted{
			MessagePosted: &orakov1.MessagePosted{ConversationId: uuid.NewString()},
		},
	}
	payload, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	h := EmailNotifier(members, mailer, "https://orako.example.com", quietLogger())
	if herr := h(message.NewMessage(uuid.NewString(), payload)); herr != nil {
		t.Fatalf("handler returned error: %v", herr)
	}

	if len(mailer.sent) != 0 {
		t.Fatalf("want 0 emails for message_posted, got %d", len(mailer.sent))
	}
}
