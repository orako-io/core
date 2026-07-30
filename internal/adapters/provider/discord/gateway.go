// SPDX-License-Identifier: AGPL-3.0-or-later

package discord

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

// GatewayIntents are the discordgo Identify intents Orako's Discord gateway
// needs: Guilds (member/guild resolution), DirectMessages (the bot's DM
// channel events), GuildMessages (conversation-thread surface messages,
// hub-and-spoke phase 4) and MessageContent. MESSAGE_CONTENT is a PRIVILEGED
// intent: it must be enabled on the bot in the Discord developer portal
// (Bot → Privileged Gateway Intents → Message Content Intent) or the gateway
// connection is rejected with close code 4014. It was not needed for DMs
// (Discord exempts DMs to the bot from that gate) but guild thread messages
// arrive with empty content without it.
const GatewayIntents = discordgo.IntentGuilds | discordgo.IntentDirectMessages |
	discordgo.IntentGuildMessages | discordgo.IntentMessageContent

// handlerTimeout bounds a single MESSAGE_CREATE handler
// invocation's DB/command work, derived from the gateway's lifecycle
// context — so one stalled dependency can never hold a handler open forever.
const handlerTimeout = 30 * time.Second

// closeDrainTimeout bounds how long Close() waits for in-flight handler
// invocations to finish before giving up (logging rather than blocking
// shutdown forever).
const closeDrainTimeout = 10 * time.Second

// gatewayMemberLookup is the narrow member-side dependency the gateway needs.
type gatewayMemberLookup interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Member, error)
	ByDiscordUserID(ctx context.Context, discordUserID string) (model.Member, error)
}

// gatewayLedgerReader resolves a DM reply's conversation. An explicit Discord
// "Reply" carries a message ref (disambiguates a pool fan-out); a plain DM
// message has none, so we fall back to the channel — a Discord DM is 1:1, so
// the channel alone identifies the open conversation (same as Teams).
type gatewayLedgerReader interface {
	ByProviderRef(ctx context.Context, channelID, messageRef string) (model.ProviderMessage, error)
	LatestByChannel(ctx context.Context, channelID string) (model.ProviderMessage, error)
}

// gatewaySurfaceReader maps a guild thread to its conversation surface
// (hub-and-spoke phase 4). *conversation.SurfaceStore satisfies it.
type gatewaySurfaceReader interface {
	ByThread(ctx context.Context, provider, threadID string) (model.ConversationSurface, error)
}

// followUpper dispatches inbound responder replies.
type followUpper interface {
	Handle(ctx context.Context, cmd command.FollowUpCommand) (command.FollowUpResult, error)
}

// attachmentIngestor downloads a human reply's attachments (from Discord's CDN
// URLs) and stores them, returning ids to link onto the follow-up.
// *inbound.Ingestor satisfies it. Optional (nil = inbound attachments off).
type attachmentIngestor interface {
	Ingest(ctx context.Context, conversationID, authorMemberID uuid.UUID, atts []service.InboundAttachment) []uuid.UUID
}

// Gateway holds the handler registered on a discordgo gateway session: DM
// replies. One Gateway's handlers may be registered
// on multiple sessions (all sharing the same bot token, see
// internal/infra/api/gatewaymgr), though in practice the supervisor keeps a 1:1
// mapping.
//
// discordgo dispatches every handler as its own fire-and-forget goroutine
// (Session.handle, unless SyncEvents is set — Orako never sets it), rooted at
// whatever context the handler itself creates; there is no ambient
// request-scoped context to inherit. So the Gateway owns a lifecycle signal
// of its own (stopCh below, deliberately a channel rather than a stored
// context.Context): each handler invocation gets a bounded, cancellable
// context tied to that signal (see beginHandler) instead of using
// context.Background(), and Close closes stopCh — immediately cancelling
// every in-flight invocation's context — then waits (bounded) for them to
// finish via wg. So in-flight DB/command work is cancellable and cannot
// outlive Close and race pool/app teardown.
type Gateway struct {
	members  gatewayMemberLookup
	ledger   gatewayLedgerReader
	surfaces gatewaySurfaceReader
	followUp followUpper
	ingestor attachmentIngestor
	logger   *slog.Logger

	stopCh chan struct{} `exhaustruct:"optional"`

	mu     sync.Mutex     `exhaustruct:"optional"`
	closed bool           `exhaustruct:"optional"`
	wg     sync.WaitGroup `exhaustruct:"optional"`
}

// NewGateway builds a Gateway. All dependencies except ingestor are required;
// a nil ingestor drops inbound attachments (the text reply still dispatches).
func NewGateway(
	members gatewayMemberLookup,
	ledger gatewayLedgerReader,
	surfaces gatewaySurfaceReader,
	followUp followUpper,
	ingestor attachmentIngestor,
	logger *slog.Logger,
) *Gateway {
	return &Gateway{
		members:  members,
		ledger:   ledger,
		surfaces: surfaces,
		followUp: followUp,
		ingestor: ingestor,
		logger:   logger,
		stopCh:   make(chan struct{}),
	}
}

// beginHandler registers one in-flight handler invocation and returns a
// context bounded by handlerTimeout AND tied to the gateway's lifecycle (it
// is cancelled the instant Close runs, even if handlerTimeout has not
// elapsed), plus a done func the handler must defer-call exactly once. ok is
// false once Close has run — the caller must drop the event without
// processing it, rather than racing wg.Add against Close's wg.Wait.
func (g *Gateway) beginHandler() (ctx context.Context, done func(), ok bool) {
	g.mu.Lock()

	if g.closed {
		g.mu.Unlock()
		return nil, nil, false
	}

	g.wg.Add(1)
	stopCh := g.stopCh

	g.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), handlerTimeout)

	// watchStop cancels ctx early if the gateway closes before handlerTimeout
	// elapses; stopWatching lets done() retire this goroutine once the
	// handler finishes normally, without waiting for either race branch.
	stopWatching := make(chan struct{})

	go func() {
		select {
		case <-stopCh:
			cancel()
		case <-stopWatching:
		}
	}()

	return ctx, func() {
		close(stopWatching)
		cancel()
		g.wg.Done()
	}, true
}

// Close closes the gateway's lifecycle signal — immediately cancelling the
// context of every in-flight handler invocation — then waits up to
// closeDrainTimeout for them to finish. Safe to call once during server
// shutdown, before the discordgo session(s) whose events it handles are
// closed (see gatewaymgr.Supervisor.Close). A second call is a no-op.
func (g *Gateway) Close() {
	g.mu.Lock()

	if g.closed {
		g.mu.Unlock()
		return
	}

	g.closed = true

	close(g.stopCh)

	g.mu.Unlock()

	drained := make(chan struct{})

	go func() {
		g.wg.Wait()
		close(drained)
	}()

	select {
	case <-drained:
	case <-time.After(closeDrainTimeout):
		g.logger.Warn("discord gateway: Close timed out waiting for in-flight handlers to drain",
			slog.Duration("timeout", closeDrainTimeout))
	}
}

// Register installs the gateway's handlers on s and sets the required
// Identify intents. Call before s.Open().
func (g *Gateway) Register(s *discordgo.Session) {
	s.Identify.Intents = GatewayIntents
	s.AddHandler(g.handleMessageCreate)
}

// handleMessageCreate funnels a DM reply into the thread: ignore bot/unknown-
// author messages, correlate the channel (and, on an explicit Discord
// "Reply", the specific message) via the delivery ledger, correlate the
// author via ByDiscordUserID, then dispatch FollowUp — the same funnel
// Slack's inbound events handler uses. The first ANSWER on a pool
// conversation stamps the descriptive responder label inside FollowUp.
func (g *Gateway) handleMessageCreate(_ *discordgo.Session, m *discordgo.MessageCreate) {
	ctx, done, ok := g.beginHandler()
	if !ok {
		// Gateway is closing/closed: drop the event rather than start new
		// work past shutdown.
		return
	}
	defer done()

	// Anti-echo hard rule (hub-and-spoke phase 5): NOTHING authored by a bot
	// or a webhook is ever ingested — the fan-out's own surface posts (plain
	// bot messages AND identity-mirrored webhook messages) must never be
	// re-mirrored, or a thread↔Slack pair would loop forever. Webhook-authored
	// messages carry Author.Bot=true on Discord, but the WebhookID check is
	// deliberate belt-and-braces: this invariant must not depend on that flag.
	if m.Author == nil || m.Author.Bot || m.WebhookID != "" {
		return
	}

	// A guild message (hub-and-spoke phase 4): only messages typed inside a
	// known conversation-thread surface are ingested; everything else the
	// GuildMessages intent delivers is silently ignored (the bot sees regular
	// channel chatter too).
	if m.GuildID != "" {
		g.handleThreadMessage(ctx, m)
		return
	}

	// A Discord DM channel is 1:1, so — unlike Slack's shared channels — the
	// channel already identifies the conversation: a plain message (no explicit
	// Reply) is a valid answer. An explicit Reply still keys the exact DM to
	// disambiguate a pool fan-out; otherwise fall back to the channel's latest
	// still-open conversation (same correlation Teams uses).
	var (
		providerMsg model.ProviderMessage
		err         error
	)

	if m.MessageReference != nil && m.MessageReference.MessageID != "" {
		providerMsg, err = g.ledger.ByProviderRef(ctx, m.ChannelID, m.MessageReference.MessageID)
	} else {
		providerMsg, err = g.ledger.LatestByChannel(ctx, m.ChannelID)
	}

	if err != nil {
		g.logger.WarnContext(ctx, "discord gateway: no open conversation for this DM",
			slog.String("channel_id", m.ChannelID), slog.Any("error", err))

		return
	}

	member, err := g.members.ByDiscordUserID(ctx, m.Author.ID)
	if err != nil {
		g.logger.WarnContext(ctx, "discord gateway: unrecognized author",
			slog.String("discord_user_id", m.Author.ID), slog.Any("error", err))

		return
	}

	attachmentIDs := g.ingestAttachments(ctx, providerMsg.ConversationID, member.ID, m)

	if _, err := g.followUp.Handle(ctx, command.FollowUpCommand{
		ConversationID: providerMsg.ConversationID,
		AuthorMemberID: member.ID,
		Message:        g.resolveMentions(ctx, m.Content, m.Mentions),
		AttachmentIDs:  attachmentIDs,
	}); err != nil {
		g.logger.ErrorContext(ctx, "discord gateway: FollowUp dispatch failed",
			slog.String("conversation_id", providerMsg.ConversationID.String()), slog.Any("error", err))

		return
	}
}

// handleThreadMessage ingests a message typed in a conversation's thread
// surface into the hub: thread → surface row → conversation; author → member
// by discord_user_id; the surface's Origin() is stamped on the message so the
// fan-out never posts it back into this same thread.
func (g *Gateway) handleThreadMessage(ctx context.Context, m *discordgo.MessageCreate) {
	surface, err := g.surfaces.ByThread(ctx, model.SurfaceProviderDiscord, m.ChannelID)
	if err != nil {
		return // not a surface thread: regular guild chatter, ignore silently.
	}

	member, err := g.members.ByDiscordUserID(ctx, m.Author.ID)
	if err != nil {
		g.logger.WarnContext(ctx, "discord gateway: unrecognized thread author",
			slog.String("discord_user_id", m.Author.ID), slog.String("thread_id", m.ChannelID))

		return
	}

	attachmentIDs := g.ingestAttachments(ctx, surface.ConversationID, member.ID, m)

	if _, err := g.followUp.Handle(ctx, command.FollowUpCommand{
		ConversationID: surface.ConversationID,
		AuthorMemberID: member.ID,
		Message:        g.resolveMentions(ctx, m.Content, m.Mentions),
		OriginSurface:  surface.Origin(),
		AttachmentIDs:  attachmentIDs,
	}); err != nil {
		g.logger.ErrorContext(ctx, "discord gateway: thread FollowUp dispatch failed",
			slog.String("conversation_id", surface.ConversationID.String()), slog.Any("error", err))
	}
}

func (g *Gateway) resolveMentions(ctx context.Context, body string, mentions []*discordgo.User) string {
	for _, user := range mentions {
		if user == nil || user.ID == "" {
			continue
		}

		member, err := g.members.ByDiscordUserID(ctx, user.ID)
		if err != nil || member.DisplayName == "" {
			continue
		}

		replacement := member.DisplayName + " (<@" + user.ID + ">)"
		body = strings.ReplaceAll(body, "<@"+user.ID+">", replacement)
		body = strings.ReplaceAll(body, "<@!"+user.ID+">", replacement)
	}

	return body
}

// ingestAttachments turns a Discord message's attachments into stored
// attachment ids via the shared ingestor. Discord CDN URLs are public (no
// bearer token), but the ingestor's download-bound + best-effort discipline
// applies identically. Returns nil when the gateway has no ingestor or the
// message carried no files.
func (g *Gateway) ingestAttachments(ctx context.Context, conversationID, authorMemberID uuid.UUID, m *discordgo.MessageCreate) []uuid.UUID {
	if g.ingestor == nil || len(m.Attachments) == 0 {
		return nil
	}

	atts := make([]service.InboundAttachment, 0, len(m.Attachments))

	for _, a := range m.Attachments {
		if a == nil || a.URL == "" {
			continue
		}

		filename := a.Filename
		if filename == "" {
			filename = "file"
		}

		mime := a.ContentType
		if mime == "" {
			mime = "application/octet-stream"
		}

		atts = append(atts, service.InboundAttachment{
			Filename:  filename,
			MimeType:  mime,
			SizeBytes: int64(a.Size),
			FetchURL:  a.URL,
		})
	}

	return g.ingestor.Ingest(ctx, conversationID, authorMemberID, atts)
}

// HandleMessageCreateForTest exposes handleMessageCreate to external tests.
// Test-only seam: discordgo's own dispatch always calls the unexported
// handler registered via Register.
func (g *Gateway) HandleMessageCreateForTest(s *discordgo.Session, m *discordgo.MessageCreate) {
	g.handleMessageCreate(s, m)
}
