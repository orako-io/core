// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
)

// fakeDirectory resolves Slack ids from a seeded email map; err forces a miss.
type fakeDirectory struct {
	byEmail map[string]string
	err     error
}

func (f *fakeDirectory) LookupSlackByEmail(_ context.Context, _ uuid.UUID, email string) (string, error) {
	if f.err != nil {
		return "", f.err
	}

	if id, ok := f.byEmail[email]; ok {
		return id, nil
	}

	return "", errors.New("users_not_found")
}

// TestAddMember_SlackAutoBind proves an invite whose email is in the Slack
// workspace stores the resolved Slack id automatically — no hand-typed id.
func TestAddMember_SlackAutoBind(t *testing.T) {
	t.Parallel()

	writer := newFakeMemberWriter()
	dir := &fakeDirectory{byEmail: map[string]string{"alice@co.com": "U123"}}
	h := MustNewAddMemberHandler(writer, fakeTransactor{}, &updateMemberFakeStore{member: baseMember()}, &fakeEventBus{}, nil, dir)

	_, err := h.Handle(t.Context(), AddMemberCommand{ProjectID: uuid.New(), Email: "alice@co.com", DisplayName: "Alice"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if got := writer.byEmail["alice@co.com"].SlackUserID; got != "U123" {
		t.Fatalf("auto-bound SlackUserID = %q, want U123", got)
	}
}

// TestAddMember_AutoBindMissStillCreates proves an email not in the workspace
// (or no Slack at all) still creates the member — binding never blocks.
func TestAddMember_AutoBindMissStillCreates(t *testing.T) {
	t.Parallel()

	writer := newFakeMemberWriter()
	dir := &fakeDirectory{err: errors.New("no slack provider")}
	h := MustNewAddMemberHandler(writer, fakeTransactor{}, &updateMemberFakeStore{member: baseMember()}, &fakeEventBus{}, nil, dir)

	_, err := h.Handle(t.Context(), AddMemberCommand{ProjectID: uuid.New(), Email: "bob@co.com", DisplayName: "Bob"})
	if err != nil {
		t.Fatalf("Handle must not fail on an auto-bind miss: %v", err)
	}

	if m, ok := writer.byEmail["bob@co.com"]; !ok || m.SlackUserID != "" {
		t.Fatalf("member should be created with no Slack binding, got %+v (ok=%v)", m, ok)
	}
}

// fakeMemberBinder is a memberBinder over one in-memory member.
type fakeMemberBinder struct {
	member  model.Member
	byIDErr error
	updated *model.Member
}

func (f *fakeMemberBinder) ByID(_ context.Context, _ uuid.UUID) (model.Member, error) {
	if f.byIDErr != nil {
		return model.Member{}, f.byIDErr
	}

	return f.member, nil
}

func (f *fakeMemberBinder) Update(_ context.Context, m model.Member) error {
	f.updated = &m

	return nil
}

// TestBindMemberChannel_Discord proves a Discord id is stored on the member,
// and that an empty id or unsupported channel is rejected.
func TestBindMemberChannel_Discord(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	store := &fakeMemberBinder{member: model.Member{ID: id, Email: "a@co.com"}}
	h := MustNewBindMemberChannelHandler(store)

	if err := h.Handle(t.Context(), BindMemberChannelCommand{MemberID: id, Channel: "discord", ExternalID: "snow-1"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if store.updated == nil || store.updated.DiscordUserID != "snow-1" {
		t.Fatalf("member DiscordUserID not bound, updated=%+v", store.updated)
	}

	if err := h.Handle(t.Context(), BindMemberChannelCommand{MemberID: id, Channel: "discord", ExternalID: ""}); err == nil {
		t.Error("empty external id must be rejected")
	}

	if err := h.Handle(t.Context(), BindMemberChannelCommand{MemberID: id, Channel: "myspace", ExternalID: "x"}); err == nil {
		t.Error("unsupported channel must be rejected")
	}
}

// fakeBackfillStore is a slackBackfillReader over an in-memory member set.
type fakeBackfillStore struct {
	missing []repository.MemberEmail
	members map[uuid.UUID]model.Member
	updated map[uuid.UUID]string // memberID → SlackUserID written
}

func (f *fakeBackfillStore) MembersMissingSlackBinding(_ context.Context, _ uuid.UUID) ([]repository.MemberEmail, error) {
	return f.missing, nil
}

func (f *fakeBackfillStore) ByID(_ context.Context, id uuid.UUID) (model.Member, error) {
	m, ok := f.members[id]
	if !ok {
		return model.Member{}, errors.New("not found")
	}

	return m, nil
}

func (f *fakeBackfillStore) Update(_ context.Context, m model.Member) error {
	if f.updated == nil {
		f.updated = map[uuid.UUID]string{}
	}

	f.updated[m.ID] = m.SlackUserID

	return nil
}

// TestSyncChatBindings_BindsUnbound proves the backfill resolves every unbound
// member found in the workspace and leaves the rest untouched.
func TestSyncChatBindings_BindsUnbound(t *testing.T) {
	t.Parallel()

	aliceID, bobID := uuid.New(), uuid.New()
	store := &fakeBackfillStore{
		missing: []repository.MemberEmail{{ID: aliceID, Email: "alice@co.com"}, {ID: bobID, Email: "bob@co.com"}},
		members: map[uuid.UUID]model.Member{
			aliceID: {ID: aliceID, Email: "alice@co.com"},
			bobID:   {ID: bobID, Email: "bob@co.com"},
		},
	}
	dir := &fakeDirectory{byEmail: map[string]string{"alice@co.com": "U123"}} // bob not in workspace

	h := MustNewSyncChatBindingsHandler(store, dir)

	res, err := h.Handle(t.Context(), SyncChatBindingsCommand{ProjectID: uuid.New()})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if res.Scanned != 2 || res.Bound != 1 {
		t.Fatalf("counts = scanned %d / bound %d, want 2 / 1", res.Scanned, res.Bound)
	}

	if store.updated[aliceID] != "U123" {
		t.Errorf("alice should be bound to U123, got %q", store.updated[aliceID])
	}

	if _, bound := store.updated[bobID]; bound {
		t.Errorf("bob is not in the workspace and must stay unbound")
	}
}
