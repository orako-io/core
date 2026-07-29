// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"context"
	"errors"
	"net/textproto"
	"strings"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
)

type fakeProjectReader struct{ project model.Project }

func (f *fakeProjectReader) ByID(_ context.Context, _ uuid.UUID) (model.Project, error) {
	return f.project, nil
}

type fakeOrgReader struct{ org model.Organization }

func (f *fakeOrgReader) ByID(_ context.Context, _ uuid.UUID) (model.Organization, error) {
	return f.org, nil
}

type fakeLinkGenerator struct {
	token    string
	linkType string
	err      error
	emails   []string
}

func (f *fakeLinkGenerator) GenerateInviteLink(_ context.Context, email string) (string, string, error) {
	f.emails = append(f.emails, email)

	return f.token, f.linkType, f.err
}

func invitedMsg(t *testing.T, memberID uuid.UUID) *message.Message {
	t.Helper()

	env := &orakov1.Envelope{
		ProjectId: uuid.NewString(),
		Type:      orakov1.EventType_EVENT_TYPE_MEMBER_LIFECYCLE,
		Payload: &orakov1.Envelope_MemberLifecycle{
			MemberLifecycle: &orakov1.MemberLifecycle{
				MemberId:   memberID.String(),
				ProjectId:  uuid.NewString(),
				Transition: orakov1.MemberTransition_MEMBER_TRANSITION_INVITED,
			},
		},
	}

	payload, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	return message.NewMessage(uuid.NewString(), payload)
}

func inviteFixtures() (*fakeMailer, *fakeMemberReader, *fakeProjectReader, *fakeOrgReader) {
	return &fakeMailer{},
		&fakeMemberReader{member: model.Member{
			ID:          uuid.New(),
			DisplayName: "Ada",
			Email:       "ada@example.com",
		}},
		&fakeProjectReader{project: model.Project{OrgID: uuid.New()}},
		&fakeOrgReader{org: model.Organization{Name: "Acme"}}
}

// TestInviteNotifier_TokenizedLink is the feature guarantee: with a link
// generator configured, the email button carries the signed /auth/callback
// URL — the click authenticates, no second verification.
func TestInviteNotifier_TokenizedLink(t *testing.T) {
	t.Parallel()

	mailer, members, projects, orgs := inviteFixtures()
	links := &fakeLinkGenerator{token: "hashed-tok-1", linkType: "invite"}

	handler := InviteNotifier(members, projects, orgs, links, mailer, "http://localhost:5173/", quietLogger())

	if err := handler(invitedMsg(t, members.member.ID)); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(mailer.sent) != 1 {
		t.Fatalf("sent %d emails, want 1", len(mailer.sent))
	}

	body := mailer.sent[0].TextBody

	if !strings.Contains(body, "http://localhost:5173/auth/callback?") {
		t.Errorf("email missing callback URL: %s", body)
	}

	if !strings.Contains(body, "token_hash=hashed-tok-1") || !strings.Contains(body, "type=invite") {
		t.Errorf("email missing token params: %s", body)
	}

	if links.emails[0] != "ada@example.com" {
		t.Errorf("generator called with %q", links.emails[0])
	}
}

// TestInviteNotifier_NoGeneratorFallsBack keeps the self-host path: no
// Supabase admin configured, the email carries the tokenless signup link.
func TestInviteNotifier_NoGeneratorFallsBack(t *testing.T) {
	t.Parallel()

	mailer, members, projects, orgs := inviteFixtures()

	handler := InviteNotifier(members, projects, orgs, nil, mailer, "http://localhost:5173", quietLogger())

	if err := handler(invitedMsg(t, members.member.ID)); err != nil {
		t.Fatalf("handler: %v", err)
	}

	body := mailer.sent[0].TextBody

	if !strings.Contains(body, "/invite?") || strings.Contains(body, "token_hash") {
		t.Errorf("fallback email should carry the tokenless /invite link: %s", body)
	}
}

// TestInviteNotifier_PermanentMailFailureDropped proves a permanent SMTP 5xx
// rejection (bad recipient) is DROPPED (handler returns nil), not retried —
// returning an error would spin the watermill poison loop that hammered
// generate_link ~47k/day in the 2026-07 invite-loop incident.
func TestInviteNotifier_PermanentMailFailureDropped(t *testing.T) {
	t.Parallel()

	mailer, members, projects, orgs := inviteFixtures()
	mailer.err = &textproto.Error{Code: 550, Msg: "no such recipient"}

	handler := InviteNotifier(members, projects, orgs, nil, mailer, "http://localhost:5173", quietLogger())

	if err := handler(invitedMsg(t, members.member.ID)); err != nil {
		t.Fatalf("permanent mail failure must be dropped (nil), got: %v", err)
	}
}

// TestInviteNotifier_TransientMailFailureRetried proves a transient failure
// (network/dial) still returns an error so the router retries it.
func TestInviteNotifier_TransientMailFailureRetried(t *testing.T) {
	t.Parallel()

	mailer, members, projects, orgs := inviteFixtures()
	mailer.err = errors.New("dial tcp: i/o timeout")

	handler := InviteNotifier(members, projects, orgs, nil, mailer, "http://localhost:5173", quietLogger())

	if err := handler(invitedMsg(t, members.member.ID)); err == nil {
		t.Fatal("transient mail failure must return an error to retry")
	}
}

// TestInviteNotifier_GeneratorFailureStillSends degrades to the fallback URL:
// the invitation must go out even when the admin API is down.
func TestInviteNotifier_GeneratorFailureStillSends(t *testing.T) {
	t.Parallel()

	mailer, members, projects, orgs := inviteFixtures()
	links := &fakeLinkGenerator{err: errors.New("admin api down")}

	handler := InviteNotifier(members, projects, orgs, links, mailer, "http://localhost:5173", quietLogger())

	if err := handler(invitedMsg(t, members.member.ID)); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(mailer.sent) != 1 {
		t.Fatalf("sent %d emails, want 1 (fallback)", len(mailer.sent))
	}

	if !strings.Contains(mailer.sent[0].TextBody, "/invite?") {
		t.Errorf("fallback email missing /invite link: %s", mailer.sent[0].TextBody)
	}
}
