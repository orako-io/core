// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

type fanoutRecorder struct{ delivered []service.OutboundMessage }

func (p *fanoutRecorder) Deliver(_ context.Context, msg service.OutboundMessage) (service.MessageRef, error) {
	p.delivered = append(p.delivered, msg)

	return service.MessageRef{}, nil
}

func (p *fanoutRecorder) ParseInbound(context.Context, []byte) (service.InboundMessage, error) {
	return service.InboundMessage{}, nil
}

type fakeFanoutReader struct {
	conv         model.Conversation
	participants []model.ConversationParticipant
	messages     []model.Message
	candidates   []uuid.UUID
}

func (f *fakeFanoutReader) ActiveCandidatesByConversations(_ context.Context, ids []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	out := map[uuid.UUID][]uuid.UUID{}
	for _, id := range ids {
		out[id] = f.candidates
	}

	return out, nil
}

func (f *fakeFanoutReader) ConversationByID(context.Context, uuid.UUID) (model.Conversation, error) {
	return f.conv, nil
}

func (f *fakeFanoutReader) ParticipantsByConversation(context.Context, uuid.UUID) ([]model.ConversationParticipant, error) {
	return f.participants, nil
}

func (f *fakeFanoutReader) MessagesByConversation(context.Context, uuid.UUID) ([]model.Message, error) {
	return f.messages, nil
}

type fakeFanoutProviders struct {
	prov       service.Provider
	noProvider map[uuid.UUID]bool
}

func (f *fakeFanoutProviders) ForMember(_ context.Context, _, memberID uuid.UUID) (service.Provider, error) {
	if f.noProvider[memberID] {
		return nil, service.ErrNoProvider
	}

	return f.prov, nil
}

type fakeFanoutMembers struct {
	names map[uuid.UUID]string
	// discordIDs marks members with a Discord binding (surface tests).
	discordIDs map[uuid.UUID]string `exhaustruct:"optional"`
}

func (f *fakeFanoutMembers) ByID(_ context.Context, id uuid.UUID) (model.Member, error) {
	return model.Member{ID: id, DisplayName: f.names[id], DiscordUserID: f.discordIDs[id]}, nil
}

func recipientSet(msgs []service.OutboundMessage) map[uuid.UUID]bool {
	out := make(map[uuid.UUID]bool, len(msgs))
	for _, d := range msgs {
		out[d.Recipient()] = true
	}

	return out
}

func messagePostedEnv(projectID, convID, authorID uuid.UUID, role orakov1.MessageRole, body string) *orakov1.Envelope {
	return &orakov1.Envelope{
		ProjectId: projectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_MESSAGE_POSTED,
		Payload: &orakov1.Envelope_MessagePosted{
			MessagePosted: &orakov1.MessagePosted{
				ConversationId: convID.String(),
				MessageId:      uuid.NewString(),
				AuthorMemberId: authorID.String(),
				Role:           role,
				Body:           body,
			},
		},
	}
}

// TestFanout_Answer_RelaysToAskerAndAddedNotAuthor proves a responder's answer
// fans out to the asker and every added participant, but never back to its
// author (the responder).
func TestFanout_Answer_RelaysToAskerAndAddedNotAuthor(t *testing.T) {
	t.Parallel()

	projectID, convID := uuid.New(), uuid.New()
	asker, responder, added := uuid.New(), uuid.New(), uuid.New()

	reader := &fakeFanoutReader{
		conv: model.Conversation{
			ID: convID, ProjectID: projectID,
			AskerMemberID: asker, ResponderMemberID: responder,
			Status: model.ConversationStatusOpen, Question: "Q?",
		},
		participants: []model.ConversationParticipant{{MemberID: added}},
	}
	prov := &fanoutRecorder{}
	providers := &fakeFanoutProviders{prov: prov}
	members := &fakeFanoutMembers{names: map[uuid.UUID]string{responder: "Jordan"}}

	h := FanoutProjector(reader, providers, members, nil, nil, nil, slog.New(slog.DiscardHandler))
	if err := h(mustEnvelopeMsg(t, messagePostedEnv(projectID, convID, responder, orakov1.MessageRole_MESSAGE_ROLE_ANSWER, "Use the composite index."))); err != nil {
		t.Fatalf("handler: %v", err)
	}

	got := recipientSet(prov.delivered)
	if len(got) != 2 || !got[asker] || !got[added] {
		t.Fatalf("recipients = %v, want asker+added only", got)
	}

	if got[responder] {
		t.Error("the author (responder) must not receive its own message")
	}

	// The author identity travels in MirrorAuthor (phase 5): identity-capable
	// adapters render it as the visible author; identity-blind ones inline it
	// via FormatOutbound's relay arm.
	if len(prov.delivered) > 0 && prov.delivered[0].MirrorAuthor != "Jordan · Slack" && prov.delivered[0].MirrorAuthor != "Jordan" {
		t.Errorf("relay should carry the author identity in MirrorAuthor, got %q", prov.delivered[0].MirrorAuthor)
	}
}

// TestFanout_CandidateReceivesOtherCandidatesAnswer is phase 1 of the
// hub-and-spoke plan: on a pool conversation, when one candidate answers
// (becoming the responder), every other still-active candidate receives the
// answer — they follow the whole thread, not just its opening question.
func TestFanout_CandidateReceivesOtherCandidatesAnswer(t *testing.T) {
	t.Parallel()

	projectID, convID := uuid.New(), uuid.New()
	asker, barbara, nathan := uuid.New(), uuid.New(), uuid.New()

	reader := &fakeFanoutReader{
		conv: model.Conversation{
			ID: convID, ProjectID: projectID,
			AskerMemberID: asker, ResponderMemberID: barbara, // Barbara claimed by replying
			Status: model.ConversationStatusAnswered, Question: "split conventionné ?",
		},
		candidates: []uuid.UUID{barbara, nathan}, // both were contacted; Nathan is still active
	}
	prov := &fanoutRecorder{}
	providers := &fakeFanoutProviders{prov: prov}
	members := &fakeFanoutMembers{names: map[uuid.UUID]string{barbara: "Barbara"}}

	h := FanoutProjector(reader, providers, members, nil, nil, nil, slog.New(slog.DiscardHandler))
	if err := h(mustEnvelopeMsg(t, messagePostedEnv(projectID, convID, barbara, orakov1.MessageRole_MESSAGE_ROLE_ANSWER, "On regroupe."))); err != nil {
		t.Fatalf("handler: %v", err)
	}

	got := recipientSet(prov.delivered)
	if !got[nathan] {
		t.Error("Nathan (still-active candidate) must receive Barbara's answer")
	}

	if !got[asker] {
		t.Error("the asker must receive the answer")
	}

	if got[barbara] {
		t.Error("the author must never receive their own message")
	}
}

// TestFanout_DashboardOnlyRecipientSkipped proves a recipient with no external
// provider (ErrNoProvider) is silently skipped, not delivered to.
func TestFanout_DashboardOnlyRecipientSkipped(t *testing.T) {
	t.Parallel()

	projectID, convID := uuid.New(), uuid.New()
	asker, responder := uuid.New(), uuid.New()

	reader := &fakeFanoutReader{
		conv: model.Conversation{
			ID: convID, ProjectID: projectID,
			AskerMemberID: asker, ResponderMemberID: responder,
			Status: model.ConversationStatusOpen,
		},
	}
	prov := &fanoutRecorder{}
	providers := &fakeFanoutProviders{prov: prov, noProvider: map[uuid.UUID]bool{asker: true}}
	members := &fakeFanoutMembers{names: map[uuid.UUID]string{}}

	h := FanoutProjector(reader, providers, members, nil, nil, nil, slog.New(slog.DiscardHandler))
	if err := h(mustEnvelopeMsg(t, messagePostedEnv(projectID, convID, responder, orakov1.MessageRole_MESSAGE_ROLE_ANSWER, "answer"))); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(prov.delivered) != 0 {
		t.Errorf("dashboard-only asker should be skipped, got %d deliveries", len(prov.delivered))
	}
}

// TestFanout_QuestionAndSystemNotRelayed proves the opening question and system
// notes are never fanned out (the ask path delivers questions; system is noise).
func TestFanout_QuestionAndSystemNotRelayed(t *testing.T) {
	t.Parallel()

	projectID, convID := uuid.New(), uuid.New()
	asker, responder := uuid.New(), uuid.New()

	reader := &fakeFanoutReader{conv: model.Conversation{
		ID: convID, ProjectID: projectID, AskerMemberID: asker, ResponderMemberID: responder, Status: model.ConversationStatusOpen,
	}}

	for _, role := range []orakov1.MessageRole{orakov1.MessageRole_MESSAGE_ROLE_QUESTION, orakov1.MessageRole_MESSAGE_ROLE_SYSTEM} {
		prov := &fanoutRecorder{}
		h := FanoutProjector(reader, &fakeFanoutProviders{prov: prov}, &fakeFanoutMembers{names: map[uuid.UUID]string{}}, nil, nil, nil, slog.New(slog.DiscardHandler))
		if err := h(mustEnvelopeMsg(t, messagePostedEnv(projectID, convID, asker, role, "x"))); err != nil {
			t.Fatalf("handler: %v", err)
		}

		if len(prov.delivered) != 0 {
			t.Errorf("role %v must not be fanned out, got %d deliveries", role, len(prov.delivered))
		}
	}
}

// TestFanout_ParticipantAdded_DeliversHistory proves a newly added member is
// delivered the thread history.
func TestFanout_ParticipantAdded_DeliversHistory(t *testing.T) {
	t.Parallel()

	projectID, convID := uuid.New(), uuid.New()
	asker, added := uuid.New(), uuid.New()

	reader := &fakeFanoutReader{
		conv: model.Conversation{ID: convID, ProjectID: projectID, AskerMemberID: asker, Status: model.ConversationStatusOpen},
		messages: []model.Message{
			{ID: uuid.New(), ConversationID: convID, AuthorMemberID: asker, Role: model.MessageRoleQuestion, Body: "Which index?"},
			{ID: uuid.New(), ConversationID: convID, AuthorMemberID: asker, Role: model.MessageRoleAnswer, Body: "Composite on (a,b)."},
		},
	}
	prov := &fanoutRecorder{}
	providers := &fakeFanoutProviders{prov: prov}
	members := &fakeFanoutMembers{names: map[uuid.UUID]string{}}

	env := &orakov1.Envelope{
		ProjectId: projectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_CONVERSATION_PARTICIPANT_ADDED,
		Payload: &orakov1.Envelope_ConversationParticipantAdded{
			ConversationParticipantAdded: &orakov1.ConversationParticipantAdded{
				ConversationId: convID.String(), MemberId: added.String(), AddedByMemberId: asker.String(),
			},
		},
	}

	h := FanoutProjector(reader, providers, members, nil, nil, nil, slog.New(slog.DiscardHandler))
	if err := h(mustEnvelopeMsg(t, env)); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(prov.delivered) != 1 || prov.delivered[0].Recipient() != added {
		t.Fatalf("want one history delivery to the added member, got %+v", prov.delivered)
	}

	if !strings.Contains(prov.delivered[0].Question, "Composite on (a,b).") {
		t.Errorf("history should include the thread, got %q", prov.delivered[0].Question)
	}
}

// TestFanout_ThreadCoveredMembersSkipDMs is phase-4 acceptance #3: members
// covered by the conversation's Discord thread surface receive the message
// via ONE post onto the thread, never a duplicate DM relay; an uncovered
// (non-guild) member still gets the DM fan-out.
func TestFanout_ThreadCoveredMembersSkipDMs(t *testing.T) {
	t.Parallel()

	projectID, convID := uuid.New(), uuid.New()
	asker, barbara, jordan := uuid.New(), uuid.New(), uuid.New() // Jordan: not on the guild

	reader := &fakeFanoutReader{
		conv: model.Conversation{
			ID: convID, ProjectID: projectID,
			AskerMemberID: asker, ResponderMemberID: barbara,
			Status: model.ConversationStatusAnswered, Question: "Q?",
		},
		candidates: []uuid.UUID{barbara, jordan},
	}

	// Surface covering asker + Barbara.
	store := newFakeSurfaceStore()
	threadProv := newFakeThreadProvider()
	surfMembers := &fakeFanoutMembers{names: map[uuid.UUID]string{barbara: "Barbara"}, discordIDs: map[uuid.UUID]string{asker: "d-a", barbara: "d-b"}}
	surfaces := newTestSurfaceManager(store, threadProv, "chan", reader.conv, surfMembers)
	surfaces.EnsureDiscordThread(t.Context(), projectID, convID, []uuid.UUID{asker, barbara, jordan})

	prov := &fanoutRecorder{}

	h := FanoutProjector(reader, &fakeFanoutProviders{prov: prov}, surfMembers, surfaces, nil, nil, slog.New(slog.DiscardHandler))
	if err := h(mustEnvelopeMsg(t, messagePostedEnv(projectID, convID, barbara, orakov1.MessageRole_MESSAGE_ROLE_ANSWER, "On regroupe."))); err != nil {
		t.Fatalf("handler: %v", err)
	}

	got := recipientSet(prov.delivered)
	if got[asker] || got[barbara] {
		t.Errorf("thread-covered members must not get DM relays, got %v", got)
	}

	if !got[jordan] {
		t.Error("Jordan (uncovered) must still get the DM relay")
	}

	// One identity-mirrored relay landed on the thread (the opening question
	// is a plain bot post; the relay posts under Barbara's identity).
	if posts := threadProv.identityPosts["thread-1"]; len(posts) != 1 || !strings.Contains(posts[0], "Barbara") {
		t.Errorf("the answer must be identity-posted once onto the thread, got %v", posts)
	}
}

// TestFanout_ThreadOriginNeverEchoesBack proves the anti-echo: a message that
// ORIGINATED from the thread surface (typed there, ingested by the gateway)
// is never posted back onto that same thread.
func TestFanout_ThreadOriginNeverEchoesBack(t *testing.T) {
	t.Parallel()

	projectID, convID := uuid.New(), uuid.New()
	asker, barbara := uuid.New(), uuid.New()

	reader := &fakeFanoutReader{
		conv: model.Conversation{
			ID: convID, ProjectID: projectID,
			AskerMemberID: asker, ResponderMemberID: barbara,
			Status: model.ConversationStatusAnswered, Question: "Q?",
		},
		candidates: []uuid.UUID{barbara},
	}

	store := newFakeSurfaceStore()
	threadProv := newFakeThreadProvider()
	surfMembers := &fakeFanoutMembers{names: map[uuid.UUID]string{barbara: "Barbara"}, discordIDs: map[uuid.UUID]string{asker: "d-a", barbara: "d-b"}}
	surfaces := newTestSurfaceManager(store, threadProv, "chan", reader.conv, surfMembers)
	surfaces.EnsureDiscordThread(t.Context(), projectID, convID, []uuid.UUID{asker, barbara})

	surface, ok := surfaces.SurfaceFor(t.Context(), convID)
	if !ok {
		t.Fatal("surface must exist")
	}

	env := messagePostedEnv(projectID, convID, barbara, orakov1.MessageRole_MESSAGE_ROLE_ANSWER, "typed in the thread")
	env.GetMessagePosted().OriginSurface = surface.Origin()

	prov := &fanoutRecorder{}

	h := FanoutProjector(reader, &fakeFanoutProviders{prov: prov}, surfMembers, surfaces, nil, nil, slog.New(slog.DiscardHandler))
	if err := h(mustEnvelopeMsg(t, env)); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if plain, identity := threadProv.posts["thread-1"], threadProv.identityPosts["thread-1"]; len(plain) != 1 || len(identity) != 0 {
		t.Errorf("a thread-originated message must not be posted back (only the opening question expected), got plain=%v identity=%v", plain, identity)
	}
}

// TestFanout_AddedParticipantCoveredByThreadSkipsHistoryDM proves a late
// participant who can join the thread gets NO history DM — the thread already
// carries the whole discussion.
func TestFanout_AddedParticipantCoveredByThreadSkipsHistoryDM(t *testing.T) {
	t.Parallel()

	projectID, convID := uuid.New(), uuid.New()
	asker, late := uuid.New(), uuid.New()

	reader := &fakeFanoutReader{
		conv: model.Conversation{ID: convID, ProjectID: projectID, AskerMemberID: asker, Status: model.ConversationStatusOpen},
		messages: []model.Message{
			{ID: uuid.New(), ConversationID: convID, AuthorMemberID: asker, Role: model.MessageRoleQuestion, Body: "Which index?"},
		},
	}

	store := newFakeSurfaceStore()
	threadProv := newFakeThreadProvider()
	surfMembers := &fakeFanoutMembers{names: map[uuid.UUID]string{}, discordIDs: map[uuid.UUID]string{asker: "d-a", late: "d-late"}}
	surfaces := newTestSurfaceManager(store, threadProv, "chan", reader.conv, surfMembers)
	surfaces.EnsureDiscordThread(t.Context(), projectID, convID, []uuid.UUID{asker})

	env := &orakov1.Envelope{
		ProjectId: projectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_CONVERSATION_PARTICIPANT_ADDED,
		Payload: &orakov1.Envelope_ConversationParticipantAdded{
			ConversationParticipantAdded: &orakov1.ConversationParticipantAdded{
				ConversationId: convID.String(), MemberId: late.String(), AddedByMemberId: asker.String(),
			},
		},
	}

	prov := &fanoutRecorder{}

	h := FanoutProjector(reader, &fakeFanoutProviders{prov: prov}, surfMembers, surfaces, nil, nil, slog.New(slog.DiscardHandler))
	if err := h(mustEnvelopeMsg(t, env)); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(prov.delivered) != 0 {
		t.Errorf("a thread-covered late participant must get no history DM, got %d", len(prov.delivered))
	}

	if surface, _ := surfaces.SurfaceFor(t.Context(), convID); !surface.Covers(late) {
		t.Error("the late participant must now be covered by the surface")
	}
}

// TestFanout_CrossSurfaceMirrorNoDuplicatesNoEcho is phase-5 acceptance #1:
// with a Discord thread + one Slack participant, a thread message reaches the
// Slack member exactly once, and the Slack member's reply lands on the thread
// exactly once — no duplicates in either direction, no echo loop.
func TestFanout_CrossSurfaceMirrorNoDuplicatesNoEcho(t *testing.T) {
	t.Parallel()

	projectID, convID := uuid.New(), uuid.New()
	asker, barbara, jordan := uuid.New(), uuid.New(), uuid.New() // Jordan on Slack

	reader := &fakeFanoutReader{
		conv: model.Conversation{
			ID: convID, ProjectID: projectID,
			AskerMemberID: asker, ResponderMemberID: barbara,
			Status: model.ConversationStatusAnswered, Question: "Q?",
		},
		candidates: []uuid.UUID{barbara, jordan},
	}

	store := newFakeSurfaceStore()
	threadProv := newFakeThreadProvider()
	surfMembers := &fakeFanoutMembers{
		names:      map[uuid.UUID]string{barbara: "Barbara", jordan: "Jordan"},
		discordIDs: map[uuid.UUID]string{asker: "d-a", barbara: "d-b"},
	}
	surfaces := newTestSurfaceManager(store, threadProv, "chan", reader.conv, surfMembers)
	surfaces.EnsureDiscordThread(t.Context(), projectID, convID, []uuid.UUID{asker, barbara, jordan})

	surface, _ := surfaces.SurfaceFor(t.Context(), convID)

	dm := &fanoutRecorder{}
	h := FanoutProjector(reader, &fakeFanoutProviders{prov: dm}, surfMembers, surfaces, nil, nil, slog.New(slog.DiscardHandler))

	// Direction 1: Barbara types IN the thread → mirrored to Jordan's Slack
	// DM exactly once, never back onto the thread.
	env := messagePostedEnv(projectID, convID, barbara, orakov1.MessageRole_MESSAGE_ROLE_ANSWER, "réponse dans le thread")
	env.GetMessagePosted().OriginSurface = surface.Origin()

	if err := h(mustEnvelopeMsg(t, env)); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(dm.delivered) != 1 || dm.delivered[0].Recipient() != jordan {
		t.Fatalf("thread message must reach Jordan exactly once, got %+v", recipientSet(dm.delivered))
	}

	if !strings.Contains(dm.delivered[0].MirrorAuthor, "Barbara") || !strings.Contains(dm.delivered[0].MirrorAuthor, "Discord") {
		t.Errorf("mirror identity = %q, want Barbara · Discord", dm.delivered[0].MirrorAuthor)
	}

	if len(threadProv.identityPosts["thread-1"]) != 0 {
		t.Error("a thread-originated message must never be posted back onto the thread")
	}

	// Direction 2: Jordan replies from Slack (no origin surface) → posted
	// onto the thread exactly once under his identity; no DM back to Jordan;
	// covered members get no DM relays.
	dm.delivered = nil

	if err := h(mustEnvelopeMsg(t, messagePostedEnv(projectID, convID, jordan, orakov1.MessageRole_MESSAGE_ROLE_ANSWER, "réponse depuis Slack"))); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(dm.delivered) != 0 {
		t.Errorf("Jordan's reply must reach the others via the thread only, got DMs to %v", recipientSet(dm.delivered))
	}

	if posts := threadProv.identityPosts["thread-1"]; len(posts) != 1 || !strings.Contains(posts[0], "Jordan") {
		t.Errorf("Jordan's reply must land on the thread exactly once under his identity, got %v", posts)
	}
}

// TestFanout_AgentAuthoredMessageReachesItsOwnAuthor closes the self-ask loop:
// a message posted by the author's AGENT (source=agent) is delivered to the
// author's own channel — the human behind the agent has not seen it — labeled
// "Name · Agent". A human-typed message (source empty/human) keeps the
// classic author exclusion (no echo).
func TestFanout_AgentAuthoredMessageReachesItsOwnAuthor(t *testing.T) {
	t.Parallel()

	projectID, convID := uuid.New(), uuid.New()
	val := uuid.New() // asker == responder == the same member (self-ask)

	reader := &fakeFanoutReader{
		conv: model.Conversation{
			ID: convID, ProjectID: projectID,
			AskerMemberID: val, ResponderMemberID: val,
			Status: model.ConversationStatusOpen, Question: "Q?",
		},
	}

	prov := &fanoutRecorder{}
	members := &fakeFanoutMembers{names: map[uuid.UUID]string{val: "Val"}}

	h := FanoutProjector(reader, &fakeFanoutProviders{prov: prov}, members, nil, nil, nil, slog.New(slog.DiscardHandler))

	// Agent-authored follow-up: Val must receive it.
	env := messagePostedEnv(projectID, convID, val, orakov1.MessageRole_MESSAGE_ROLE_FOLLOW_UP, "précision de l'agent")
	env.GetMessagePosted().Source = "agent"

	if err := h(mustEnvelopeMsg(t, env)); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(prov.delivered) != 1 || prov.delivered[0].Recipient() != val {
		t.Fatalf("agent-authored message must reach its own author, got %+v", recipientSet(prov.delivered))
	}

	if prov.delivered[0].MirrorAuthor != "Val · Agent" {
		t.Errorf("mirror label = %q, want \"Val · Agent\"", prov.delivered[0].MirrorAuthor)
	}

	// Human-typed reply from Val (e.g. from Discord): never echoed back.
	prov.delivered = nil

	human := messagePostedEnv(projectID, convID, val, orakov1.MessageRole_MESSAGE_ROLE_FOLLOW_UP, "réponse tapée par Val")
	human.GetMessagePosted().Source = "human"

	if err := h(mustEnvelopeMsg(t, human)); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(prov.delivered) != 0 {
		t.Errorf("a human-typed message must not echo back to its author, got %+v", recipientSet(prov.delivered))
	}
}
