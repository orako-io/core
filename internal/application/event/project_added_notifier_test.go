// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
)

type chanMailer struct{ sent chan model.EmailMessage }

func (m *chanMailer) Send(_ context.Context, msg model.EmailMessage) error {
	m.sent <- msg

	return nil
}

type fakeMemberByID struct{ member model.Member }

func (f *fakeMemberByID) ByID(context.Context, uuid.UUID) (model.Member, error) {
	return f.member, nil
}

type fakeProjectByID struct{ byID map[uuid.UUID]model.Project }

func (f *fakeProjectByID) ByID(_ context.Context, id uuid.UUID) (model.Project, error) {
	return f.byID[id], nil
}

type fakeOrgByID struct{ org model.Organization }

func (f *fakeOrgByID) ByID(context.Context, uuid.UUID) (model.Organization, error) {
	return f.org, nil
}

func addedEnv(t *testing.T, memberID, projectID uuid.UUID) *orakov1.Envelope {
	t.Helper()

	return &orakov1.Envelope{
		ProjectId: projectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_MEMBER_LIFECYCLE,
		Payload: &orakov1.Envelope_MemberLifecycle{
			MemberLifecycle: &orakov1.MemberLifecycle{
				MemberId:   memberID.String(),
				ProjectId:  projectID.String(),
				Transition: orakov1.MemberTransition_MEMBER_TRANSITION_ADDED_TO_PROJECT,
			},
		},
	}
}

// TestProjectAddedNotifier_CoalescesMultiProjectAdd proves that two
// ADDED_TO_PROJECT events for the same member inside the window produce exactly
// ONE email that names both projects — never one email per project.
func TestProjectAddedNotifier_CoalescesMultiProjectAdd(t *testing.T) {
	t.Parallel()

	memberID, orgID := uuid.New(), uuid.New()
	p1, p2 := uuid.New(), uuid.New()

	member, _ := model.NewMember(memberID, "val@example.com", "Val")
	member.Status = model.MemberStatusActive

	mailer := &chanMailer{sent: make(chan model.EmailMessage, 4)}
	n := NewProjectAddedNotifier(
		&fakeMemberByID{member: member},
		&fakeProjectByID{byID: map[uuid.UUID]model.Project{
			p1: {ID: p1, Name: "atlas", OrgID: orgID},
			p2: {ID: p2, Name: "borealis", OrgID: orgID},
		}},
		&fakeOrgByID{org: model.Organization{ID: orgID, Name: "uxia"}},
		mailer,
		"https://app.orako.io",
		30*time.Millisecond,
		slog.New(slog.DiscardHandler),
	)

	h := n.Handler()
	if err := h(mustEnvelopeMsg(t, addedEnv(t, memberID, p1))); err != nil {
		t.Fatalf("handler p1: %v", err)
	}

	if err := h(mustEnvelopeMsg(t, addedEnv(t, memberID, p2))); err != nil {
		t.Fatalf("handler p2: %v", err)
	}

	select {
	case msg := <-mailer.sent:
		if msg.To != "val@example.com" {
			t.Errorf("To = %q", msg.To)
		}

		if !strings.Contains(msg.TextBody, "atlas") || !strings.Contains(msg.TextBody, "borealis") {
			t.Errorf("email must name both projects, got %q", msg.TextBody)
		}

		if !strings.Contains(msg.TextBody, "uxia") {
			t.Errorf("email must name the org, got %q", msg.TextBody)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no email sent within the window")
	}

	// No second email may follow.
	select {
	case extra := <-mailer.sent:
		t.Fatalf("exactly one coalesced email expected, got a second: %q", extra.Subject)
	case <-time.After(150 * time.Millisecond):
	}
}
