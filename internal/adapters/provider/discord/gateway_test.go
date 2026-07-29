// SPDX-License-Identifier: AGPL-3.0-or-later

package discord_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/provider/discord"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// fakeIngestor records the inbound attachments it was handed and returns a
// fixed set of ids, standing in for the shared *inbound.Ingestor.
type fakeIngestor struct {
	gotFilenames []string
	ids          []uuid.UUID
}

func (f *fakeIngestor) Ingest(_ context.Context, _, _ uuid.UUID, atts []service.InboundAttachment) []uuid.UUID {
	for _, a := range atts {
		f.gotFilenames = append(f.gotFilenames, a.Filename)
	}

	return f.ids
}

// TestGateway_ThreadMessage_IngestsAttachments proves a human's thread reply
// carrying a Discord attachment resolves each file through the ingestor and
// threads the resulting ids into the FollowUp command that appends the reply.
func TestGateway_ThreadMessage_IngestsAttachments(t *testing.T) {
	t.Parallel()

	members := newFakeMemberStore()
	authorID := uuid.New()
	members.add(model.Member{ID: authorID, DiscordUserID: "discord-author-1"})

	convID := uuid.New()
	surfaces := newFakeSurfaces()
	surfaces.byThread["thread-1"] = model.ConversationSurface{
		ConversationID: convID, Provider: model.SurfaceProviderDiscord, ThreadID: "thread-1",
	}

	attID := uuid.New()
	ing := &fakeIngestor{ids: []uuid.UUID{attID}}
	followUp := &fakeFollowUpper{}

	gw := discord.NewGateway(members, newFakeLedger(), surfaces, followUp, ing, discardLogger())

	msg := &discordgo.MessageCreate{Message: &discordgo.Message{
		ChannelID: "thread-1",
		GuildID:   "guild-1",
		Content:   "here's the screenshot",
		Author:    &discordgo.User{ID: "discord-author-1", Bot: false},
		Attachments: []*discordgo.MessageAttachment{{
			URL: "https://cdn.discordapp.com/attachments/x/shot.png", Filename: "shot.png",
			ContentType: "image/png", Size: 2048,
		}},
	}}

	gw.HandleMessageCreateForTest(nil, msg)

	if len(ing.gotFilenames) != 1 || ing.gotFilenames[0] != "shot.png" {
		t.Fatalf("ingestor filenames = %v, want [shot.png]", ing.gotFilenames)
	}

	if len(followUp.last.AttachmentIDs) != 1 || followUp.last.AttachmentIDs[0] != attID {
		t.Fatalf("FollowUp AttachmentIDs = %v, want [%s]", followUp.last.AttachmentIDs, attID)
	}
}

// TestGateway_HandleMessageCreate_ExplicitReplyFunnel proves a DM reply that
// explicitly replies to a tracked ledger message correlates channel + author
// and dispatches FollowUp.
func TestGateway_HandleMessageCreate_ExplicitReplyFunnel(t *testing.T) {
	t.Parallel()

	members := newFakeMemberStore()
	authorID := uuid.New()
	members.add(model.Member{ID: authorID, DiscordUserID: "discord-author-1"})

	ledger := newFakeLedger()
	convID := uuid.New()
	ledger.add("dm-channel-1", "msg-1", model.ProviderMessage{ConversationID: convID})

	followUp := &fakeFollowUpper{}

	gw := discord.NewGateway(members, ledger, newFakeSurfaces(), followUp, nil, discardLogger())

	msg := &discordgo.MessageCreate{Message: &discordgo.Message{
		ChannelID: "dm-channel-1",
		Content:   "here's my answer",
		Author:    &discordgo.User{ID: "discord-author-1", Bot: false},
		MessageReference: &discordgo.MessageReference{
			MessageID: "msg-1",
		},
	}}

	gw.HandleMessageCreateForTest(nil, msg)

	if followUp.last.ConversationID != convID {
		t.Errorf("FollowUp ConversationID: got %v, want %v", followUp.last.ConversationID, convID)
	}

	if followUp.last.AuthorMemberID != authorID {
		t.Errorf("FollowUp AuthorMemberID: got %v, want %v", followUp.last.AuthorMemberID, authorID)
	}

	if followUp.last.Message != "here's my answer" {
		t.Errorf("FollowUp Message: got %q, want %q", followUp.last.Message, "here's my answer")
	}
}

// TestGateway_HandleMessageCreate_PlainDMReply_Correlates proves a plain DM
// message (no explicit Discord Reply) still correlates via the channel's latest
// open conversation and dispatches FollowUp — a Discord DM is 1:1, so the
// channel alone identifies the conversation.
func TestGateway_HandleMessageCreate_PlainDMReply_Correlates(t *testing.T) {
	t.Parallel()

	members := newFakeMemberStore()
	authorID := uuid.New()
	members.add(model.Member{ID: authorID, DiscordUserID: "discord-author-1"})

	ledger := newFakeLedger()
	convID := uuid.New()
	ledger.add("dm-channel-1", "msg-1", model.ProviderMessage{ConversationID: convID})

	followUp := &fakeFollowUpper{}
	gw := discord.NewGateway(members, ledger, newFakeSurfaces(), followUp, nil, discardLogger())

	// A plain message typed into the DM — no MessageReference.
	msg := &discordgo.MessageCreate{Message: &discordgo.Message{
		ChannelID: "dm-channel-1",
		Content:   "reply typed directly in the DM",
		Author:    &discordgo.User{ID: "discord-author-1", Bot: false},
	}}

	gw.HandleMessageCreateForTest(nil, msg)

	if followUp.last.ConversationID != convID {
		t.Errorf("FollowUp ConversationID: got %v, want %v", followUp.last.ConversationID, convID)
	}

	if followUp.last.Message != "reply typed directly in the DM" {
		t.Errorf("FollowUp Message: got %q", followUp.last.Message)
	}
}

// TestGateway_HandleMessageCreate_IgnoresBotsAndUnknowns proves bot messages,
// plain messages in a channel with no open conversation, unknown channels, and
// unknown authors are all silently ignored (no FollowUp dispatch).
func TestGateway_HandleMessageCreate_IgnoresBotsAndUnknowns(t *testing.T) {
	t.Parallel()

	members := newFakeMemberStore()
	members.add(model.Member{ID: uuid.New(), DiscordUserID: "discord-author-1"})

	ledger := newFakeLedger()
	ledger.add("dm-channel-1", "msg-1", model.ProviderMessage{ConversationID: uuid.New()})

	cases := []struct {
		name string
		msg  *discordgo.MessageCreate
	}{
		{
			name: "bot_author",
			msg: &discordgo.MessageCreate{Message: &discordgo.Message{
				ChannelID:        "dm-channel-1",
				Author:           &discordgo.User{ID: "discord-author-1", Bot: true},
				MessageReference: &discordgo.MessageReference{MessageID: "msg-1"},
			}},
		},
		{
			name: "plain_message_no_open_conversation",
			msg: &discordgo.MessageCreate{Message: &discordgo.Message{
				ChannelID: "unknown-channel",
				Author:    &discordgo.User{ID: "discord-author-1", Bot: false},
			}},
		},
		{
			name: "unknown_channel",
			msg: &discordgo.MessageCreate{Message: &discordgo.Message{
				ChannelID:        "unknown-channel",
				Author:           &discordgo.User{ID: "discord-author-1", Bot: false},
				MessageReference: &discordgo.MessageReference{MessageID: "msg-1"},
			}},
		},
		{
			name: "unknown_author",
			msg: &discordgo.MessageCreate{Message: &discordgo.Message{
				ChannelID:        "dm-channel-1",
				Author:           &discordgo.User{ID: "unknown-user", Bot: false},
				MessageReference: &discordgo.MessageReference{MessageID: "msg-1"},
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			followUp := &fakeFollowUpper{}
			gw := discord.NewGateway(members, ledger, newFakeSurfaces(), followUp, nil, discardLogger())

			gw.HandleMessageCreateForTest(nil, tc.msg)

			if len(followUp.calls) != 0 {
				t.Errorf("FollowUp must not be dispatched, got %+v", followUp.last)
			}
		})
	}
}

// TestGateway_Close_WaitsForInFlightHandlerAndCancelsItsContext proves the
// review fix (🟡 #10): Close() waits for an in-flight handler invocation to
// finish (asserted via gatedFollowUpper) rather than returning immediately,
// and the per-event context that invocation is running with is derived from
// the gateway's lifecycle context — so it observes cancellation once Close
// runs, rather than being rooted at an uncancellable context.Background().
func TestGateway_Close_WaitsForInFlightHandlerAndCancelsItsContext(t *testing.T) {
	t.Parallel()

	members := newFakeMemberStore()
	authorID := uuid.New()
	members.add(model.Member{ID: authorID, DiscordUserID: "discord-author-1"})

	ledger := newFakeLedger()
	convID := uuid.New()
	ledger.add("dm-channel-1", "msg-1", model.ProviderMessage{ConversationID: convID})

	gated := newGatedFollowUpper()

	gw := discord.NewGateway(members, ledger, newFakeSurfaces(), gated, nil, discardLogger())

	msg := &discordgo.MessageCreate{Message: &discordgo.Message{
		ChannelID: "dm-channel-1",
		Content:   "here's my answer",
		Author:    &discordgo.User{ID: "discord-author-1", Bot: false},
		MessageReference: &discordgo.MessageReference{
			MessageID: "msg-1",
		},
	}}

	handlerDone := make(chan struct{})

	go func() {
		gw.HandleMessageCreateForTest(nil, msg)
		close(handlerDone)
	}()

	select {
	case <-gated.started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never reached FollowUp.Handle")
	}

	closeDone := make(chan struct{})

	go func() {
		gw.Close()
		close(closeDone)
	}()

	// Close must block while the handler is still in flight, not return
	// immediately (that would leave DB/command work racing teardown).
	select {
	case <-closeDone:
		t.Fatal("Close() returned before the in-flight handler finished")
	case <-time.After(200 * time.Millisecond):
	}

	close(gated.release)

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not return after the in-flight handler finished")
	}

	<-handlerDone

	if gated.ctxErr == nil {
		t.Error("in-flight handler's context must observe cancellation once Close() runs, got nil ctx.Err()")
	}
}

// TestGateway_Close_DropsHandlerInvocationsAfterward proves a handler
// invocation that arrives after Close() has already run is dropped rather
// than dispatched — Close is not merely advisory.
func TestGateway_Close_DropsHandlerInvocationsAfterward(t *testing.T) {
	t.Parallel()

	members := newFakeMemberStore()
	authorID := uuid.New()
	members.add(model.Member{ID: authorID, DiscordUserID: "discord-author-1"})

	ledger := newFakeLedger()
	ledger.add("dm-channel-1", "msg-1", model.ProviderMessage{ConversationID: uuid.New()})

	followUp := &fakeFollowUpper{}
	gw := discord.NewGateway(members, ledger, newFakeSurfaces(), followUp, nil, discardLogger())

	gw.Close()

	msg := &discordgo.MessageCreate{Message: &discordgo.Message{
		ChannelID: "dm-channel-1",
		Content:   "too late",
		Author:    &discordgo.User{ID: "discord-author-1", Bot: false},
		MessageReference: &discordgo.MessageReference{
			MessageID: "msg-1",
		},
	}}

	gw.HandleMessageCreateForTest(nil, msg)

	if len(followUp.calls) != 0 {
		t.Errorf("FollowUp must not be dispatched after Close(), got %+v", followUp.last)
	}
}

// TestGateway_ThreadMessageIngestedToHub is phase-4 acceptance #2: a message
// typed in a conversation's thread surface reaches the hub as a FollowUp with
// the right conversation, the author resolved by discord_user_id, and the
// surface's origin stamped (so the fan-out never echoes it back).
func TestGateway_ThreadMessageIngestedToHub(t *testing.T) {
	t.Parallel()

	convID := uuid.New()
	memberID := uuid.New()

	members := newFakeMemberStore()
	members.add(model.Member{ID: memberID, DiscordUserID: "discord-user-9"})

	surfaces := newFakeSurfaces()
	surfaces.byThread["thread-42"] = model.ConversationSurface{
		ID:             uuid.New(),
		ConversationID: convID,
		Provider:       model.SurfaceProviderDiscord,
		Kind:           model.SurfaceKindThread,
		ChannelID:      "chan-1",
		ThreadID:       "thread-42",
	}

	followUp := &fakeFollowUpper{}

	gw := discord.NewGateway(members, newFakeLedger(), surfaces, followUp, nil, discardLogger())
	t.Cleanup(gw.Close)

	gw.HandleMessageCreateForTest(nil, &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID:   "guild-1",
		ChannelID: "thread-42",
		Content:   "On regroupe les retraits.",
		Author:    &discordgo.User{ID: "discord-user-9", Bot: false},
	}})

	if len(followUp.calls) != 1 {
		t.Fatalf("want 1 FollowUp dispatch, got %d", len(followUp.calls))
	}

	got := followUp.calls[0]
	if got.ConversationID != convID || got.AuthorMemberID != memberID || got.Message != "On regroupe les retraits." {
		t.Errorf("FollowUp = %+v, want conv/author/body from the thread message", got)
	}

	if got.OriginSurface != "discord:thread:thread-42" {
		t.Errorf("OriginSurface = %q, want the surface origin key", got.OriginSurface)
	}

	// Source is left empty → NewMessage's conservative default (human): a
	// thread message is typed by a person.
	if got.Source != "" {
		t.Errorf("Source = %q, want empty (defaults to human)", got.Source)
	}
}

// TestGateway_GuildChatterOutsideSurfacesIgnored proves regular guild
// messages (no surface row) are silently dropped — the GuildMessages intent
// delivers all channel chatter the bot can see.
func TestGateway_GuildChatterOutsideSurfacesIgnored(t *testing.T) {
	t.Parallel()

	members := newFakeMemberStore()
	followUp := &fakeFollowUpper{}

	gw := discord.NewGateway(members, newFakeLedger(), newFakeSurfaces(), followUp, nil, discardLogger())
	t.Cleanup(gw.Close)

	gw.HandleMessageCreateForTest(nil, &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID:   "guild-1",
		ChannelID: "random-channel",
		Content:   "lunch?",
		Author:    &discordgo.User{ID: "someone", Bot: false},
	}})

	if len(followUp.calls) != 0 {
		t.Errorf("guild chatter outside surfaces must not dispatch, got %d calls", len(followUp.calls))
	}
}

// TestGateway_BotThreadMessageIgnored proves the anti-echo floor: the bot's
// own surface posts (fan-out relays) are never re-ingested.
func TestGateway_BotThreadMessageIgnored(t *testing.T) {
	t.Parallel()

	surfaces := newFakeSurfaces()
	surfaces.byThread["thread-42"] = model.ConversationSurface{
		ID: uuid.New(), ConversationID: uuid.New(),
		Provider: model.SurfaceProviderDiscord, Kind: model.SurfaceKindThread,
		ChannelID: "chan-1", ThreadID: "thread-42",
	}

	followUp := &fakeFollowUpper{}

	gw := discord.NewGateway(newFakeMemberStore(), newFakeLedger(), surfaces, followUp, nil, discardLogger())
	t.Cleanup(gw.Close)

	gw.HandleMessageCreateForTest(nil, &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID:   "guild-1",
		ChannelID: "thread-42",
		Content:   "💬 Barbara: relayed",
		Author:    &discordgo.User{ID: "bot-id", Bot: true},
	}})

	if len(followUp.calls) != 0 {
		t.Errorf("bot-authored thread messages must be ignored, got %d calls", len(followUp.calls))
	}
}

// TestGateway_WebhookThreadMessageIgnored is the phase-5 synthetic-loop
// regression: an identity-mirrored webhook message (as the fan-out itself
// posts) is never re-ingested, even if the author.bot flag were somehow
// unset — the WebhookID check alone must break the loop.
func TestGateway_WebhookThreadMessageIgnored(t *testing.T) {
	t.Parallel()

	surfaces := newFakeSurfaces()
	surfaces.byThread["thread-42"] = model.ConversationSurface{
		ID: uuid.New(), ConversationID: uuid.New(),
		Provider: model.SurfaceProviderDiscord, Kind: model.SurfaceKindThread,
		ChannelID: "chan-1", ThreadID: "thread-42",
	}

	followUp := &fakeFollowUpper{}

	gw := discord.NewGateway(newFakeMemberStore(), newFakeLedger(), surfaces, followUp, nil, discardLogger())
	t.Cleanup(gw.Close)

	gw.HandleMessageCreateForTest(nil, &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID:   "guild-1",
		ChannelID: "thread-42",
		Content:   "💬 Jordan · Slack: mirrored",
		WebhookID: "webhook-7",
		Author:    &discordgo.User{ID: "webhook-7", Bot: false}, // deliberately NOT flagged bot
	}})

	if len(followUp.calls) != 0 {
		t.Errorf("webhook-authored messages must never be ingested (echo loop), got %d calls", len(followUp.calls))
	}
}
