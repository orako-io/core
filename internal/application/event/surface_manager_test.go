// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/provider"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

// fakeSurfaceStore is an in-memory surfaceStore.
type fakeSurfaceStore struct {
	byConv   map[uuid.UUID]model.ConversationSurface
	archived []uuid.UUID
}

func newFakeSurfaceStore() *fakeSurfaceStore {
	return &fakeSurfaceStore{byConv: map[uuid.UUID]model.ConversationSurface{}}
}

func (f *fakeSurfaceStore) Create(_ context.Context, s model.ConversationSurface) (bool, error) {
	if _, ok := f.byConv[s.ConversationID]; ok {
		return false, nil
	}

	f.byConv[s.ConversationID] = s

	return true, nil
}

func (f *fakeSurfaceStore) ByConversationProvider(_ context.Context, convID uuid.UUID, _ string) (model.ConversationSurface, error) {
	if s, ok := f.byConv[convID]; ok {
		return s, nil
	}

	return model.ConversationSurface{}, adaptererr.ErrNotFound
}

func (f *fakeSurfaceStore) AddCoveredMember(_ context.Context, surfaceID, memberID uuid.UUID) error {
	for convID, s := range f.byConv {
		if s.ID == surfaceID {
			s.CoveredMemberIDs = append(s.CoveredMemberIDs, memberID)
			f.byConv[convID] = s
		}
	}

	return nil
}

func (f *fakeSurfaceStore) Archive(_ context.Context, surfaceID uuid.UUID) (bool, error) {
	if slices.Contains(f.archived, surfaceID) {
		return false, nil
	}

	f.archived = append(f.archived, surfaceID)

	return true, nil
}

// fakeThreadProvider implements Provider + ThreadSurfacer + ChannelPoster +
// IdentityPoster and records every thread operation.
type fakeThreadProvider struct {
	createdName    string
	createdChannel string
	threadID       string
	invited        []string
	failInvites    map[string]bool // discord user ids whose invite fails (not on the guild)
	posts          map[string][]string
	// identityPosts records PostAsIdentity calls per thread as "username|text".
	identityPosts  map[string][]string
	archivedThread string
	deletedThread  string
	failCreate     bool
	// failIdentity simulates a missing Manage Webhooks permission.
	failIdentity bool
}

func newFakeThreadProvider() *fakeThreadProvider {
	return &fakeThreadProvider{
		threadID:      "thread-1",
		posts:         map[string][]string{},
		identityPosts: map[string][]string{},
		failInvites:   map[string]bool{},
	}
}

func (f *fakeThreadProvider) PostAsIdentity(_ context.Context, _, threadID, username, text string) error {
	if f.failIdentity {
		return errors.New("missing Manage Webhooks permission")
	}

	f.identityPosts[threadID] = append(f.identityPosts[threadID], username+"|"+text)

	return nil
}

func (f *fakeThreadProvider) Deliver(context.Context, service.OutboundMessage) (service.MessageRef, error) {
	return service.MessageRef{}, nil
}

func (f *fakeThreadProvider) ParseInbound(context.Context, []byte) (service.InboundMessage, error) {
	return service.InboundMessage{}, service.ErrUnrecognizedMessage
}

func (f *fakeThreadProvider) CreateThread(_ context.Context, parentChannelID, name string) (string, error) {
	if f.failCreate {
		return "", errors.New("thread creation refused")
	}

	f.createdChannel, f.createdName = parentChannelID, name

	return f.threadID, nil
}

func (f *fakeThreadProvider) AddThreadMember(_ context.Context, _, platformUserID string) error {
	if f.failInvites[platformUserID] {
		return errors.New("user not on guild")
	}

	f.invited = append(f.invited, platformUserID)

	return nil
}

func (f *fakeThreadProvider) ArchiveThread(_ context.Context, threadID string) error {
	f.archivedThread = threadID

	return nil
}

func (f *fakeThreadProvider) DeleteThread(_ context.Context, threadID string) error {
	f.deletedThread = threadID

	return nil
}

func (f *fakeThreadProvider) PostChannel(_ context.Context, channelID, text string) (service.MessageRef, error) {
	f.posts[channelID] = append(f.posts[channelID], text)

	return service.MessageRef{ChannelID: channelID, MessageID: "m"}, nil
}

// fakeSurfaceDeps bundles the remaining SurfaceManager ports.
type fakeSurfaceAnchors struct {
	rows []service.ProviderAlertChannel
}

func (f *fakeSurfaceAnchors) ConfiguredProvidersWithAlertChannel(context.Context, uuid.UUID) ([]service.ProviderAlertChannel, error) {
	return f.rows, nil
}

type fakeKindProviders struct{ prov service.Provider }

func (f *fakeKindProviders) ForProjectKind(context.Context, uuid.UUID, provider.ProviderKind) (service.Provider, error) {
	if f.prov == nil {
		return nil, service.ErrNoProvider
	}

	return f.prov, nil
}

type fakeSurfaceConvs struct{ conv model.Conversation }

func (f *fakeSurfaceConvs) ConversationByID(context.Context, uuid.UUID) (model.Conversation, error) {
	return f.conv, nil
}

// newTestSurfaceManager wires a SurfaceManager over the fakes.
func newTestSurfaceManager(
	store *fakeSurfaceStore,
	prov service.Provider,
	anchor string,
	conv model.Conversation,
	members *fakeFanoutMembers,
) *SurfaceManager {
	rows := []service.ProviderAlertChannel{}
	if anchor != "" {
		rows = append(rows, service.ProviderAlertChannel{Kind: "discord", AlertChannelIDs: []string{anchor}})
	}

	return NewSurfaceManager(store, members, &fakeSurfaceConvs{conv: conv}, &fakeSurfaceAnchors{rows: rows}, &fakeKindProviders{prov: prov}, nil, nil, slog.New(slog.DiscardHandler))
}

// TestSurface_PoolAskOpensOneThread is phase-4 acceptance #1: a pool ask
// among guild members produces ONE private thread in the project channel,
// named after the conversation title, with the question posted in it and
// every invitable member covered.
func TestSurface_PoolAskOpensOneThread(t *testing.T) {
	t.Parallel()

	projectID, convID := uuid.New(), uuid.New()
	asker, barbara, nathan := uuid.New(), uuid.New(), uuid.New()

	store := newFakeSurfaceStore()
	prov := newFakeThreadProvider()
	members := &fakeFanoutMembers{names: map[uuid.UUID]string{}, discordIDs: map[uuid.UUID]string{
		asker: "d-asker", barbara: "d-barbara", nathan: "d-nathan",
	}}
	conv := model.Conversation{ID: convID, ProjectID: projectID, AskerMemberID: asker, Title: "Retrait file active", Question: "Q?", Status: model.ConversationStatusOpen}

	m := newTestSurfaceManager(store, prov, "chan-cfea", conv, members)

	covered := m.EnsureDiscordThread(t.Context(), projectID, convID, []uuid.UUID{asker, barbara, nathan})

	if prov.createdChannel != "chan-cfea" || prov.createdName != "Retrait file active" {
		t.Errorf("thread created in %q named %q; want project channel + title", prov.createdChannel, prov.createdName)
	}

	if len(covered) != 3 || !covered[asker] || !covered[barbara] || !covered[nathan] {
		t.Errorf("covered = %v, want all three guild members", covered)
	}

	if posts := prov.posts["thread-1"]; len(posts) != 1 {
		t.Errorf("the opening question must be posted once in the thread, got %d posts", len(posts))
	}

	// Replay (duplicate OPENED): the existing surface short-circuits — no
	// second thread, no second question post.
	again := m.EnsureDiscordThread(t.Context(), projectID, convID, []uuid.UUID{asker, barbara, nathan})
	if len(again) != 3 || len(prov.posts["thread-1"]) != 1 {
		t.Errorf("replay must reuse the surface: covered=%v posts=%d", again, len(prov.posts["thread-1"]))
	}
}

// TestSurface_NonGuildMemberStaysUncovered proves a member whose thread
// invite fails (not on the guild) — or who has no Discord binding — keeps the
// DM path: they are not in the covered set.
func TestSurface_NonGuildMemberStaysUncovered(t *testing.T) {
	t.Parallel()

	projectID, convID := uuid.New(), uuid.New()
	barbara, jordan, noDiscord := uuid.New(), uuid.New(), uuid.New()

	store := newFakeSurfaceStore()
	prov := newFakeThreadProvider()
	prov.failInvites["d-jordan"] = true // Jordan is on Slack, not this guild

	members := &fakeFanoutMembers{names: map[uuid.UUID]string{}, discordIDs: map[uuid.UUID]string{
		barbara: "d-barbara", jordan: "d-jordan",
	}}
	conv := model.Conversation{ID: convID, ProjectID: projectID, AskerMemberID: barbara, Title: "t", Question: "Q?", Status: model.ConversationStatusOpen}

	m := newTestSurfaceManager(store, prov, "chan", conv, members)

	covered := m.EnsureDiscordThread(t.Context(), projectID, convID, []uuid.UUID{barbara, jordan, noDiscord})

	if !covered[barbara] {
		t.Error("Barbara (guild member) must be covered")
	}

	if covered[jordan] || covered[noDiscord] {
		t.Errorf("Jordan (invite failed) and the unbound member must stay uncovered: %v", covered)
	}
}

// TestSurface_NoAnchorMeansNoSurface proves the DM fallback: without a
// configured Discord alert channel there is no thread and nobody is covered.
func TestSurface_NoAnchorMeansNoSurface(t *testing.T) {
	t.Parallel()

	convID := uuid.New()
	m := newTestSurfaceManager(newFakeSurfaceStore(), newFakeThreadProvider(), "", model.Conversation{ID: convID, Status: model.ConversationStatusOpen}, &fakeFanoutMembers{names: map[uuid.UUID]string{}})

	if covered := m.EnsureDiscordThread(t.Context(), uuid.New(), convID, []uuid.UUID{uuid.New()}); len(covered) != 0 {
		t.Errorf("no anchor channel must mean no surface, got covered=%v", covered)
	}
}

// TestSurface_CloseArchivesThread is the second half of acceptance #1: close
// posts the resolution into the thread and archives it, exactly once.
func TestSurface_CloseArchivesThread(t *testing.T) {
	t.Parallel()

	projectID, convID := uuid.New(), uuid.New()
	member := uuid.New()

	store := newFakeSurfaceStore()
	prov := newFakeThreadProvider()
	members := &fakeFanoutMembers{names: map[uuid.UUID]string{}, discordIDs: map[uuid.UUID]string{member: "d-1"}}
	conv := model.Conversation{ID: convID, ProjectID: projectID, AskerMemberID: member, Title: "t", Question: "Q?", Status: model.ConversationStatusOpen}

	m := newTestSurfaceManager(store, prov, "chan", conv, members)
	m.EnsureDiscordThread(t.Context(), projectID, convID, []uuid.UUID{member})

	m.CloseSurface(t.Context(), projectID, convID, "✅ Resolved by X: done")

	if prov.archivedThread != "thread-1" {
		t.Errorf("close must archive the thread, got %q", prov.archivedThread)
	}

	if posts := prov.posts["thread-1"]; len(posts) != 2 || posts[1] != "✅ Resolved by X: done" {
		t.Errorf("close must post the resolution before archiving, got %v", posts)
	}

	// Duplicate CLOSED replay: the Archive CAS makes it a no-op.
	prov.archivedThread = ""
	m.CloseSurface(t.Context(), projectID, convID, "✅ again")

	if prov.archivedThread != "" || len(prov.posts["thread-1"]) != 2 {
		t.Error("a duplicate close must not re-post or re-archive")
	}
}

// TestSurface_MirrorPostsUnderOriginIdentity is phase-5 acceptance #2: a
// message mirrored onto the thread is posted under the origin member's
// visible identity (webhook username), platform-suffixed.
func TestSurface_MirrorPostsUnderOriginIdentity(t *testing.T) {
	t.Parallel()

	projectID, convID := uuid.New(), uuid.New()
	member := uuid.New()

	store := newFakeSurfaceStore()
	prov := newFakeThreadProvider()
	members := &fakeFanoutMembers{names: map[uuid.UUID]string{}, discordIDs: map[uuid.UUID]string{member: "d-1"}}
	conv := model.Conversation{ID: convID, ProjectID: projectID, AskerMemberID: member, Title: "t", Question: "Q?", Status: model.ConversationStatusOpen}

	m := newTestSurfaceManager(store, prov, "chan", conv, members)
	m.EnsureDiscordThread(t.Context(), projectID, convID, []uuid.UUID{member})

	surface, ok := m.SurfaceFor(t.Context(), convID)
	if !ok {
		t.Fatal("surface must exist")
	}

	m.PostToSurface(t.Context(), projectID, surface, "Jordan · Slack", "voici ma réponse", nil)

	if got := prov.identityPosts["thread-1"]; len(got) != 1 || got[0] != "Jordan · Slack|voici ma réponse" {
		t.Errorf("identity post = %v, want the origin member's platform-suffixed identity", got)
	}
}

// TestSurface_IdentityFailureDegradesToPlainBotPost is phase-5 acceptance #3:
// a missing webhook permission degrades the mirror to a plain bot post
// carrying the identity inline — never to silence.
func TestSurface_IdentityFailureDegradesToPlainBotPost(t *testing.T) {
	t.Parallel()

	projectID, convID := uuid.New(), uuid.New()
	member := uuid.New()

	store := newFakeSurfaceStore()
	prov := newFakeThreadProvider()
	prov.failIdentity = true

	members := &fakeFanoutMembers{names: map[uuid.UUID]string{}, discordIDs: map[uuid.UUID]string{member: "d-1"}}
	conv := model.Conversation{ID: convID, ProjectID: projectID, AskerMemberID: member, Title: "t", Question: "Q?", Status: model.ConversationStatusOpen}

	m := newTestSurfaceManager(store, prov, "chan", conv, members)
	m.EnsureDiscordThread(t.Context(), projectID, convID, []uuid.UUID{member})

	surface, _ := m.SurfaceFor(t.Context(), convID)
	m.PostToSurface(t.Context(), projectID, surface, "Jordan · Slack", "voici ma réponse", nil)

	posts := prov.posts["thread-1"] // opening question + the degraded mirror
	if len(posts) != 2 || !strings.Contains(posts[1], "Jordan · Slack") || !strings.Contains(posts[1], "voici ma réponse") {
		t.Errorf("identity failure must degrade to a plain bot post with the label inline, got %v", posts)
	}
}

// TestSurface_DeleteSurfaceDeletesThread proves an org-admin conversation
// delete takes the platform thread with it.
func TestSurface_DeleteSurfaceDeletesThread(t *testing.T) {
	t.Parallel()

	projectID, convID := uuid.New(), uuid.New()
	member := uuid.New()

	store := newFakeSurfaceStore()
	prov := newFakeThreadProvider()
	members := &fakeFanoutMembers{names: map[uuid.UUID]string{}, discordIDs: map[uuid.UUID]string{member: "d-1"}}
	conv := model.Conversation{ID: convID, ProjectID: projectID, AskerMemberID: member, Title: "t", Question: "Q?", Status: model.ConversationStatusOpen}

	m := newTestSurfaceManager(store, prov, "chan", conv, members)
	m.EnsureDiscordThread(t.Context(), projectID, convID, []uuid.UUID{member})

	m.DeleteSurface(t.Context(), projectID, convID)

	if prov.deletedThread != "thread-1" {
		t.Errorf("deleting the conversation must delete its thread, got %q", prov.deletedThread)
	}

	// No surface → silent no-op.
	m.DeleteSurface(t.Context(), projectID, uuid.New())
}
