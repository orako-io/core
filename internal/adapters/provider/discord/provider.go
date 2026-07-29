// SPDX-License-Identifier: AGPL-3.0-or-later

package discord

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

var (
	_ service.Provider                 = (*Provider)(nil)
	_ service.Editor                   = (*Provider)(nil)
	_ service.ChannelPoster            = (*Provider)(nil)
	_ service.ThreadSurfacer           = (*Provider)(nil)
	_ service.IdentityPoster           = (*Provider)(nil)
	_ service.ChannelAttachmentPoster  = (*Provider)(nil)
	_ service.IdentityAttachmentPoster = (*Provider)(nil)
)

// discordCodeCannotMessageUser is the Discord API JSON error code returned
// when a DM is blocked by the recipient's privacy settings (e.g. "Cannot send
// messages to this user" / DMs disabled for non-friends).
const discordCodeCannotMessageUser = 50007

// discordMaxContentRunes is Discord's hard cap on a message Content field:
// 2000 characters, counted as Unicode code points (Discord counts runes, not
// bytes). A body over it is rejected with HTTP 400 / JSON error code 50035
// ("Invalid Form Body"). A longer body is SPLIT into sequential messages
// (service.SplitForDelivery) — humans never get truncated content
// (hub-and-spoke phase 3; the claim-era single-message-ref Edit constraint
// that forced truncation is gone).
const discordMaxContentRunes = 2000

// discordMaxAttachmentBytes bounds a single outbound file download from its
// signed URL before it is re-uploaded to Discord as multipart. Discord's own
// non-boosted upload ceiling is 25 MiB; anything larger it would reject anyway.
const discordMaxAttachmentBytes = 25 << 20

// ErrDMBlocked is returned by Deliver/Edit/PostChannel when Discord rejects a
// DM due to the recipient's privacy settings. A typed sentinel so callers
// (the delivery notifier's failure path) can log a specific cause; the
// fallback-to-email behavior itself does not depend on distinguishing this
// from any other Deliver failure.
var ErrDMBlocked = errors.New("discord: DM blocked by recipient privacy settings")

// memberLookup is the narrow member-side dependency the Discord adapter needs.
type memberLookup interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Member, error)
	ByDiscordUserID(ctx context.Context, discordUserID string) (model.Member, error)
}

// webhookCache persists the one-webhook-per-channel pair identity mirroring
// uses (hub-and-spoke phase 5). *conversation.SurfaceStore satisfies it. nil
// disables identity posting (PostAsIdentity then always errors, and callers
// fall back to plain bot posts).
type webhookCache interface {
	WebhookForChannel(ctx context.Context, channelID string) (webhookID, token string, err error)
	SaveChannelWebhook(ctx context.Context, channelID, webhookID, token string) error
}

// Provider is the real Discord messaging adapter (REST only; see gatewaymgr
// for the persistent gateway connection inbound replies and button clicks
// arrive over).
type Provider struct {
	cfg      Config
	session  *discordgo.Session
	members  memberLookup
	webhooks webhookCache
}

// New builds a Discord Provider. members is required; webhooks may be nil for
// callers that never exercise identity mirroring.
func New(cfg Config, members memberLookup, webhooks webhookCache) *Provider {
	session, err := newSession(cfg)
	if err != nil {
		// discordgo.New only fails on a malformed token argument shape, never
		// on network I/O (no connection is made for a REST-only session); a
		// Provider that cannot even construct its session is a programming
		// error the caller must fix, consistent with other adapters' fail-fast
		// construction (for example MustNewXHandler panicking on nil deps).
		panic(fmt.Sprintf("discord: building session: %v", err))
	}

	return &Provider{cfg: cfg, session: session, members: members, webhooks: webhooks}
}

// Deliver opens (or reuses) a DM channel with the recipient's Discord binding
// and sends the message as plain text — split into sequential messages when it
// exceeds Discord's cap; the returned ref is the LAST message's (inbound reply
// correlation keys on it, and a plain DM falls back to the channel anyway).
func (p *Provider) Deliver(ctx context.Context, msg service.OutboundMessage) (service.MessageRef, error) {
	recipient := msg.Recipient()

	member, err := p.members.ByID(ctx, recipient)
	if err != nil {
		return service.MessageRef{}, fmt.Errorf("discord deliver: resolving responder: %w", err)
	}

	if member.DiscordUserID == "" {
		return service.MessageRef{}, fmt.Errorf("discord deliver: responder %s has no Discord user ID bound", recipient)
	}

	channel, err := p.session.UserChannelCreate(member.DiscordUserID, discordgo.WithContext(ctx))
	if err != nil {
		return service.MessageRef{}, wrapDiscordErr("creating DM channel", err)
	}

	convURL := service.ConversationURL(p.cfg.DashboardBaseURL, msg.ConversationID)
	chunks := service.SplitForDelivery(service.FormatOutbound(msg, convURL), discordMaxContentRunes)

	ref, err := service.SendSplit(ctx, chunks, func(ctx context.Context, chunk string) (service.MessageRef, error) {
		send := &discordgo.MessageSend{Content: chunk, Embeds: nil}

		sent, err := p.session.ChannelMessageSendComplex(channel.ID, send, discordgo.WithContext(ctx))
		if err != nil {
			return service.MessageRef{}, wrapDiscordErr("sending DM", err)
		}

		return service.MessageRef{ChannelID: channel.ID, MessageID: sent.ID}, nil
	})
	if err != nil {
		return service.MessageRef{}, err
	}

	// Attachments follow the text as a separate message carrying the files as
	// multipart uploads (the bytes are pulled from each signed URL). Best-effort
	// per file: a download that fails is skipped, never blocking the reply.
	if blobs := p.downloadAttachments(ctx, msg.Attachments); len(blobs) > 0 {
		sent, err := p.session.ChannelMessageSendComplex(channel.ID, &discordgo.MessageSend{Files: discordFiles(blobs)}, discordgo.WithContext(ctx))
		if err != nil {
			return service.MessageRef{}, wrapDiscordErr("sending DM attachments", err)
		}

		ref = service.MessageRef{ChannelID: channel.ID, MessageID: sent.ID}
	}

	return ref, nil
}

// attachmentBlob is one downloaded outbound attachment held in memory so fresh
// multipart readers can be built for each send attempt (a webhook retry would
// otherwise re-read an exhausted reader).
type attachmentBlob struct {
	name        string
	contentType string
	data        []byte
}

// downloadAttachments pulls each outbound attachment's bytes from its signed
// URL. Best-effort: a download error (or an over-cap file) is logged and
// skipped rather than failing the whole delivery — the text has already gone
// through, and the dashboard keeps the full copy. Returns nil when there are
// none.
func (p *Provider) downloadAttachments(ctx context.Context, atts []service.OutboundAttachment) []attachmentBlob {
	if len(atts) == 0 {
		return nil
	}

	blobs := make([]attachmentBlob, 0, len(atts))

	for _, a := range atts {
		content, err := p.downloadAttachment(ctx, a.URL)
		if err != nil {
			// Never log the URL — a signed URL is a bearer credential.
			slog.WarnContext(ctx, "discord: downloading outbound attachment",
				slog.String("filename", a.Filename), slog.Any("error", err))

			continue
		}

		blobs = append(blobs, attachmentBlob{name: a.Filename, contentType: a.MimeType, data: content})
	}

	return blobs
}

// discordFiles builds a fresh set of discordgo.File readers from the downloaded
// blobs — call it once per send attempt so a retry never re-reads an exhausted
// reader.
func discordFiles(blobs []attachmentBlob) []*discordgo.File {
	files := make([]*discordgo.File, 0, len(blobs))

	for _, b := range blobs {
		files = append(files, &discordgo.File{
			Name:        b.name,
			ContentType: b.contentType,
			Reader:      bytes.NewReader(b.data),
		})
	}

	return files
}

// attachmentDownloadTimeout bounds an attachment fetch; http.DefaultClient has no
// timeout of its own (D1).
const attachmentDownloadTimeout = 30 * time.Second

// downloadClient is a shared, timeout-bound client for attachment fetches.
var downloadClient = &http.Client{Timeout: attachmentDownloadTimeout} //nolint:gochecknoglobals // shared timeout-bound client by design

// downloadAttachment fetches a signed attachment URL, bounded to
// discordMaxAttachmentBytes. The URL is never logged (it is a bearer
// credential). A body over the cap is rejected rather than truncated.
func (p *Provider) downloadAttachment(ctx context.Context, signedURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building download request: %w", err)
	}

	resp, err := downloadClient.Do(req)
	if err != nil {
		return nil, errors.New("attachment download failed") // omit err: URL may be embedded
	}

	defer resp.Body.Close() //nolint:errcheck // informational close error in defer

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("attachment download: unexpected status %d", resp.StatusCode)
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, discordMaxAttachmentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading attachment body: %w", err)
	}

	if len(content) > discordMaxAttachmentBytes {
		return nil, fmt.Errorf("attachment exceeds %d-byte limit", discordMaxAttachmentBytes)
	}

	return content, nil
}

// Edit rewrites a previously delivered message via a message edit.
func (p *Provider) Edit(ctx context.Context, ref service.MessageRef, text string) error {
	edit := discordgo.NewMessageEdit(ref.ChannelID, ref.MessageID)
	edit.SetContent(text)

	// Always clear components: legacy messages delivered before the claim
	// teardown may still carry a Claim button; an edit retires it.
	components := []discordgo.MessageComponent{}
	edit.Components = &components

	if _, err := p.session.ChannelMessageEditComplex(edit, discordgo.WithContext(ctx)); err != nil {
		return wrapDiscordErr("editing message", err)
	}

	return nil
}

// PostChannel posts text to a named Discord channel (e.g. the project's
// alert channel) rather than DMing a member; over-cap text is split, never
// truncated.
func (p *Provider) PostChannel(ctx context.Context, channelID, text string) (service.MessageRef, error) {
	return service.SendSplit(ctx, service.SplitForDelivery(text, discordMaxContentRunes),
		func(ctx context.Context, chunk string) (service.MessageRef, error) {
			sent, err := p.session.ChannelMessageSend(channelID, chunk, discordgo.WithContext(ctx))
			if err != nil {
				return service.MessageRef{}, wrapDiscordErr("posting to channel", err)
			}

			return service.MessageRef{ChannelID: channelID, MessageID: sent.ID}, nil
		})
}

// ParseInbound always returns ErrUnrecognizedMessage: Discord has no inbound
// webhook for DMs. Replies arrive exclusively over the gateway (see
// gateway.go's MESSAGE_CREATE handler, supervised by
// internal/infra/api/gatewaymgr), which correlates and dispatches directly
// without going through this port method.
func (p *Provider) ParseInbound(_ context.Context, _ []byte) (service.InboundMessage, error) {
	return service.InboundMessage{}, service.ErrUnrecognizedMessage
}

// threadAutoArchiveMinutes is the Discord auto-archive window for a
// conversation thread: 3 days (4320 min) of inactivity — the escalation
// ticker and explicit closure normally settle a conversation well before.
const threadAutoArchiveMinutes = 4320

// threadNameMaxRunes is Discord's cap on a thread name (100 characters).
const threadNameMaxRunes = 100

// CreateThread opens a PRIVATE thread named name in parentChannelID and
// returns its id. Requires the bot permissions Create Private Threads +
// Send Messages in Threads + Manage Threads on the parent channel.
func (p *Provider) CreateThread(ctx context.Context, parentChannelID, name string) (string, error) {
	if runes := []rune(name); len(runes) > threadNameMaxRunes {
		name = string(runes[:threadNameMaxRunes-1]) + "…"
	}

	thread, err := p.session.ThreadStartComplex(parentChannelID, &discordgo.ThreadStart{
		Name:                name,
		AutoArchiveDuration: threadAutoArchiveMinutes,
		Type:                discordgo.ChannelTypeGuildPrivateThread,
		Invitable:           false,
	}, discordgo.WithContext(ctx))
	if err != nil {
		return "", wrapDiscordErr("creating thread", err)
	}

	return thread.ID, nil
}

// AddThreadMember invites a Discord user into the thread. Fails when the user
// does not share the guild (the caller falls back to their DM).
func (p *Provider) AddThreadMember(ctx context.Context, threadID, platformUserID string) error {
	if err := p.session.ThreadMemberAdd(threadID, platformUserID, discordgo.WithContext(ctx)); err != nil {
		return wrapDiscordErr("adding thread member", err)
	}

	return nil
}

// ArchiveThread archives the thread (conversation closed).
func (p *Provider) ArchiveThread(ctx context.Context, threadID string) error {
	archived := true
	if _, err := p.session.ChannelEditComplex(threadID, &discordgo.ChannelEdit{
		Archived: &archived,
	}, discordgo.WithContext(ctx)); err != nil {
		return wrapDiscordErr("archiving thread", err)
	}

	return nil
}

// DeleteThread permanently deletes the thread (a thread is a channel;
// ChannelDelete with Manage Threads removes it).
func (p *Provider) DeleteThread(ctx context.Context, threadID string) error {
	if _, err := p.session.ChannelDelete(threadID, discordgo.WithContext(ctx)); err != nil {
		return wrapDiscordErr("deleting thread", err)
	}

	return nil
}

// orakoWebhookName is the display name of the per-channel webhook Orako
// creates for identity mirroring; the per-message username override is what
// readers actually see.
const orakoWebhookName = "Orako"

// PostAsIdentity posts text into threadID with username as the visible author,
// via the parent channel's cached webhook — created lazily on first use
// (requires the Manage Webhooks permission). Any failure is returned so the
// caller degrades to a plain bot post: a mirror must never be silently lost.
func (p *Provider) PostAsIdentity(ctx context.Context, parentChannelID, threadID, username, text string) error {
	if p.webhooks == nil {
		return errors.New("discord identity post: no webhook cache configured")
	}

	webhookID, token, err := p.channelWebhook(ctx, parentChannelID)
	if err != nil {
		return err
	}

	execute := func(id, tok string) error {
		var execErr error

		for _, chunk := range service.SplitForDelivery(text, discordMaxContentRunes) {
			params := &discordgo.WebhookParams{}
			params.Content, params.Username = chunk, username

			if _, err := p.session.WebhookThreadExecute(id, tok, false, threadID, params, discordgo.WithContext(ctx)); err != nil {
				execErr = err

				break
			}
		}

		return execErr
	}

	if err := execute(webhookID, token); err != nil {
		// The cached webhook may have been deleted server-side: recreate once
		// and retry before giving up.
		freshID, freshToken, createErr := p.createChannelWebhook(ctx, parentChannelID)
		if createErr != nil {
			return wrapDiscordErr("executing webhook", err)
		}

		if err := execute(freshID, freshToken); err != nil {
			return wrapDiscordErr("executing webhook", err)
		}
	}

	return nil
}

// PostChannelWithFiles posts text into channelID (a channel or thread id) and
// uploads the attachments as multipart files on the same message. Over-cap
// text is split; the files ride the final chunk. Best-effort downloads: a file
// that cannot be fetched is skipped (the text still posts).
func (p *Provider) PostChannelWithFiles(ctx context.Context, channelID, text string, atts []service.OutboundAttachment) (service.MessageRef, error) {
	blobs := p.downloadAttachments(ctx, atts)
	chunks := service.SplitForDelivery(text, discordMaxContentRunes)

	// A files-only post (empty text) still needs one message to carry them.
	if len(chunks) == 0 {
		chunks = []string{""}
	}

	var ref service.MessageRef

	for i, chunk := range chunks {
		send := &discordgo.MessageSend{Content: chunk}
		if i == len(chunks)-1 {
			send.Files = discordFiles(blobs)
		}

		sent, err := p.session.ChannelMessageSendComplex(channelID, send, discordgo.WithContext(ctx))
		if err != nil {
			return service.MessageRef{}, wrapDiscordErr("posting to channel with files", err)
		}

		ref = service.MessageRef{ChannelID: channelID, MessageID: sent.ID}
	}

	return ref, nil
}

// PostAsIdentityWithFiles is PostAsIdentity plus multipart file uploads: it
// posts text AND the attachments into threadID under username, via the parent
// channel's webhook. The files ride the final text chunk; fresh readers are
// built per attempt so the recreate-and-retry path (stale webhook) re-uploads
// cleanly. Any failure is returned so the caller degrades to a plain file post.
func (p *Provider) PostAsIdentityWithFiles(ctx context.Context, parentChannelID, threadID, username, text string, atts []service.OutboundAttachment) error {
	if p.webhooks == nil {
		return errors.New("discord identity post: no webhook cache configured")
	}

	webhookID, token, err := p.channelWebhook(ctx, parentChannelID)
	if err != nil {
		return err
	}

	blobs := p.downloadAttachments(ctx, atts)

	execute := func(id, tok string) error {
		chunks := service.SplitForDelivery(text, discordMaxContentRunes)
		if len(chunks) == 0 {
			chunks = []string{""}
		}

		for i, chunk := range chunks {
			params := &discordgo.WebhookParams{Content: chunk, Username: username}
			if i == len(chunks)-1 {
				params.Files = discordFiles(blobs)
			}

			if _, err := p.session.WebhookThreadExecute(id, tok, false, threadID, params, discordgo.WithContext(ctx)); err != nil {
				return err
			}
		}

		return nil
	}

	if err := execute(webhookID, token); err != nil {
		freshID, freshToken, createErr := p.createChannelWebhook(ctx, parentChannelID)
		if createErr != nil {
			return wrapDiscordErr("executing webhook with files", err)
		}

		if err := execute(freshID, freshToken); err != nil {
			return wrapDiscordErr("executing webhook with files", err)
		}
	}

	return nil
}

// channelWebhook resolves the channel's cached webhook pair, creating (and
// caching) one on first use.
func (p *Provider) channelWebhook(ctx context.Context, channelID string) (string, string, error) {
	if webhookID, token, err := p.webhooks.WebhookForChannel(ctx, channelID); err == nil {
		return webhookID, token, nil
	}

	return p.createChannelWebhook(ctx, channelID)
}

// createChannelWebhook creates the channel webhook and caches the pair.
func (p *Provider) createChannelWebhook(ctx context.Context, channelID string) (string, string, error) {
	webhook, err := p.session.WebhookCreate(channelID, orakoWebhookName, "", discordgo.WithContext(ctx))
	if err != nil {
		return "", "", wrapDiscordErr("creating webhook", err)
	}

	if err := p.webhooks.SaveChannelWebhook(ctx, channelID, webhook.ID, webhook.Token); err != nil {
		return "", "", err
	}

	return webhook.ID, webhook.Token, nil
}

// wrapDiscordErr wraps a discordgo REST error, translating a privacy-blocked
// DM into ErrDMBlocked so callers can recognize the cause without inspecting
// Discord's numeric error code themselves.
func wrapDiscordErr(action string, err error) error {
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Message != nil && restErr.Message.Code == discordCodeCannotMessageUser {
		return fmt.Errorf("discord %s: %w: %w", action, ErrDMBlocked, err)
	}

	return fmt.Errorf("discord %s: %w", action, err)
}
