// SPDX-License-Identifier: AGPL-3.0-or-later

package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

var _ service.Provider = (*Provider)(nil)

// telegramMaxMessageRunes is the Telegram Bot API sendMessage text cap: 4096
// UTF-8 characters (code points). A longer body is rejected with a 400
// "message is too long"; the body is truncated to a dashboard pointer instead.
const telegramMaxMessageRunes = 4096

// memberLookup is the narrow member-side dependency the Telegram adapter needs.
type memberLookup interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Member, error)
	ByTelegramChatID(ctx context.Context, telegramChatID string) (model.Member, error)
}

// conversationAccess is the narrow conversation-side dependency.
type conversationAccess interface {
	ConversationByTelegramMessage(ctx context.Context, chatID, messageID string) (model.Conversation, error)
	SetTelegramThread(ctx context.Context, id uuid.UUID, chatID, messageID string) error
}

// Provider is the real Telegram Bot API messaging adapter. Inbound correlation
// requires reply_to_message: the responder must reply to the bot's outbound
// message, not send a new top-level message.
type Provider struct {
	cfg     Config
	members memberLookup
	convs   conversationAccess
}

// New builds a Telegram Provider from the given config and backing stores.
func New(cfg Config, members memberLookup, convs conversationAccess) *Provider {
	return &Provider{cfg: cfg, members: members, convs: convs}
}

// sendMessageRequest is the JSON body sent to sendMessage.
type sendMessageRequest struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

// sendMessageResult is the result object inside a successful sendMessage response.
type sendMessageResult struct {
	MessageID int64 `json:"message_id"`
}

// sendMessageResponse is the Telegram Bot API response envelope for sendMessage.
type sendMessageResponse struct {
	OK          bool              `json:"ok"`
	Result      sendMessageResult `json:"result"`
	Description string            `json:"description"`
}

// Deliver sends the question to the responder via a Telegram DM and records
// the returned chat_id + message_id so inbound replies can be correlated.
func (p *Provider) Deliver(ctx context.Context, msg service.OutboundMessage) (service.MessageRef, error) {
	recipient := msg.Recipient()

	member, err := p.members.ByID(ctx, recipient)
	if err != nil {
		return service.MessageRef{}, fmt.Errorf("telegram deliver: resolving responder: %w", err)
	}

	if member.TelegramChatID == "" {
		return service.MessageRef{}, fmt.Errorf("telegram deliver: responder %s has no Telegram chat ID bound", recipient)
	}

	convURL := service.ConversationURL(p.cfg.DashboardBaseURL, msg.ConversationID)
	chunks := service.SplitForDelivery(service.FormatOutbound(msg, convURL), telegramMaxMessageRunes)

	ref, err := service.SendSplit(ctx, chunks, func(ctx context.Context, chunk string) (service.MessageRef, error) {
		resp, err := p.sendMessage(ctx, member.TelegramChatID, chunk)
		if err != nil {
			return service.MessageRef{}, fmt.Errorf("telegram deliver: sending message: %w", err)
		}

		return service.MessageRef{
			ChannelID: member.TelegramChatID,
			MessageID: strconv.FormatInt(resp.Result.MessageID, 10),
		}, nil
	})
	if err != nil {
		return service.MessageRef{}, err
	}

	messageID := ref.MessageID

	// The direct-ask path records the conversation-level thread binding used by
	// ConversationByTelegramMessage. A pool candidate (RecipientMemberID set,
	// ResponderMemberID still unassigned) is not recorded here: it is tracked
	// solely through the provider_messages ledger by the delivery notifier.
	if msg.RecipientMemberID == uuid.Nil {
		if err := p.convs.SetTelegramThread(ctx, msg.ConversationID, member.TelegramChatID, messageID); err != nil {
			return service.MessageRef{}, fmt.Errorf("telegram deliver: recording thread binding: %w", err)
		}
	}

	// Attachments follow the text, best-effort: a photo via sendPhoto (richer
	// inline preview), any other file via sendDocument. Telegram fetches the
	// signed URL itself. A failure here never fails the whole delivery — the
	// text and the dashboard copy already carry the message.
	for _, att := range msg.Attachments {
		if err := p.sendAttachment(ctx, member.TelegramChatID, att); err != nil {
			return service.MessageRef{}, fmt.Errorf("telegram deliver: sending attachment %q: %w", att.Filename, err)
		}
	}

	return service.MessageRef{ChannelID: member.TelegramChatID, MessageID: messageID}, nil
}

// sendAttachment delivers one attachment by URL: sendPhoto for an image,
// sendDocument otherwise. Telegram fetches the (signed) URL server-side.
func (p *Provider) sendAttachment(ctx context.Context, chatID string, att service.OutboundAttachment) error {
	method, field := "sendDocument", "document"
	if att.IsImage() {
		method, field = "sendPhoto", "photo"
	}

	body, err := json.Marshal(map[string]string{"chat_id": chatID, field: att.URL})
	if err != nil {
		return fmt.Errorf("encoding request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/bot%s/%s", p.cfg.apiBase(), p.cfg.BotToken, method)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := p.cfg.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", sanitizeTransportError(err))
	}

	defer resp.Body.Close() //nolint:errcheck // response body close errors are informational in defer

	var result sendMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	if !result.OK {
		return fmt.Errorf("telegram API error: %s", result.Description)
	}

	return nil
}

// sendMessage POSTs to the Bot API sendMessage endpoint and parses the response.
func (p *Provider) sendMessage(ctx context.Context, chatID, text string) (sendMessageResponse, error) {
	body, err := json.Marshal(sendMessageRequest{ChatID: chatID, Text: text})
	if err != nil {
		return sendMessageResponse{}, fmt.Errorf("encoding request: %w", err)
	}

	url := fmt.Sprintf("%s/bot%s/sendMessage", p.cfg.apiBase(), p.cfg.BotToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return sendMessageResponse{}, fmt.Errorf("building request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := p.cfg.httpClient().Do(req)
	if err != nil {
		return sendMessageResponse{}, fmt.Errorf("sending request: %w", sanitizeTransportError(err))
	}

	defer resp.Body.Close() //nolint:errcheck // response body close errors are informational in defer

	var result sendMessageResponse

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return sendMessageResponse{}, fmt.Errorf("decoding response: %w", err)
	}

	if !result.OK {
		return sendMessageResponse{}, fmt.Errorf("telegram API error: %s", result.Description)
	}

	return result, nil
}

// telegramUpdate is the outer envelope of a Telegram Bot API Update; only the
// fields relevant to inbound reply correlation are decoded.
type telegramUpdate struct {
	Message *telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID      int64            `json:"message_id"`
	From           telegramUser     `json:"from"`
	Chat           telegramChat     `json:"chat"`
	Text           string           `json:"text"`
	Caption        string           `json:"caption"`
	Photo          []telegramPhoto  `json:"photo"`
	Document       *telegramDoc     `json:"document"`
	ReplyToMessage *telegramMessage `json:"reply_to_message"`
}

// telegramPhoto is one size of a photo message (Telegram sends several; the
// last is the largest).
type telegramPhoto struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

// telegramDoc is a non-image file attachment.
type telegramDoc struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

// getFileResult is the relevant subset of the Bot API getFile response.
type getFileResponse struct {
	OK     bool `json:"ok"`
	Result struct {
		FilePath string `json:"file_path"`
	} `json:"result"`
}

type telegramUser struct {
	ID int64 `json:"id"`
}

type telegramChat struct {
	ID int64 `json:"id"`
}

// ParseInbound maps a raw Telegram Update to a normalized InboundMessage:
// chat.id + reply_to_message.message_id → conversation, from.id → member.
// Returns service.ErrUnrecognizedMessage for non-message updates, missing
// reply_to_message, or unmatched conversation/member.
func (p *Provider) ParseInbound(ctx context.Context, raw []byte) (service.InboundMessage, error) {
	var update telegramUpdate

	if err := json.Unmarshal(raw, &update); err != nil {
		return service.InboundMessage{}, fmt.Errorf("telegram parse inbound: unmarshal: %w", err)
	}

	if update.Message == nil {
		return service.InboundMessage{}, service.ErrUnrecognizedMessage
	}

	if update.Message.ReplyToMessage == nil {
		return service.InboundMessage{}, service.ErrUnrecognizedMessage
	}

	chatID := strconv.FormatInt(update.Message.Chat.ID, 10)
	replyToMessageID := strconv.FormatInt(update.Message.ReplyToMessage.MessageID, 10)

	conv, err := p.convs.ConversationByTelegramMessage(ctx, chatID, replyToMessageID)
	if err != nil {
		return service.InboundMessage{}, fmt.Errorf("telegram parse inbound: correlating conversation: %w", err)
	}

	fromChatID := strconv.FormatInt(update.Message.From.ID, 10)

	member, err := p.members.ByTelegramChatID(ctx, fromChatID)
	if err != nil {
		return service.InboundMessage{}, fmt.Errorf("telegram parse inbound: correlating member: %w", err)
	}

	// A media message carries its text in `caption`, not `text`.
	body := update.Message.Text
	if body == "" {
		body = update.Message.Caption
	}

	attachments, err := p.resolveInboundAttachments(ctx, update.Message)
	if err != nil {
		return service.InboundMessage{}, fmt.Errorf("telegram parse inbound: resolving attachments: %w", err)
	}

	return service.InboundMessage{
		ConversationID: conv.ID,
		AuthorMemberID: member.ID,
		Body:           body,
		ProjectID:      conv.ProjectID,
		Attachments:    attachments,
	}, nil
}

// resolveInboundAttachments turns a message's photo/document into inbound
// attachments with a token-bearing FetchURL (the inbound handler downloads it
// once). A photo carries several sizes — the last is the largest.
func (p *Provider) resolveInboundAttachments(ctx context.Context, m *telegramMessage) ([]service.InboundAttachment, error) {
	var out []service.InboundAttachment

	if len(m.Photo) > 0 {
		largest := m.Photo[len(m.Photo)-1]

		att, err := p.inboundFile(ctx, largest.FileID, "image.jpg", "image/jpeg", largest.FileSize)
		if err != nil {
			return nil, err
		}

		out = append(out, att)
	}

	if m.Document != nil {
		filename := m.Document.FileName
		if filename == "" {
			filename = "file"
		}

		mime := m.Document.MimeType
		if mime == "" {
			mime = "application/octet-stream"
		}

		att, err := p.inboundFile(ctx, m.Document.FileID, filename, mime, m.Document.FileSize)
		if err != nil {
			return nil, err
		}

		out = append(out, att)
	}

	return out, nil
}

// inboundFile resolves a Telegram file_id to a downloadable InboundAttachment
// via getFile. The FetchURL carries the bot token — the inbound handler must
// download it immediately and never persist or log it.
func (p *Provider) inboundFile(ctx context.Context, fileID, filename, mime string, size int64) (service.InboundAttachment, error) {
	filePath, err := p.getFilePath(ctx, fileID)
	if err != nil {
		return service.InboundAttachment{}, err
	}

	//nolint:nosprintfhostport // Telegram file API path; host+token+path is the documented form
	fetchURL := fmt.Sprintf("%s/file/bot%s/%s", p.cfg.apiBase(), p.cfg.BotToken, filePath)

	return service.InboundAttachment{
		Filename:  filename,
		MimeType:  mime,
		SizeBytes: size,
		FetchURL:  fetchURL,
	}, nil
}

// getFilePath calls the Bot API getFile to resolve a file_id's storage path.
func (p *Provider) getFilePath(ctx context.Context, fileID string) (string, error) {
	endpoint := fmt.Sprintf("%s/bot%s/getFile?file_id=%s", p.cfg.apiBase(), p.cfg.BotToken, url.QueryEscape(fileID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("building getFile request: %w", err)
	}

	resp, err := p.cfg.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("getFile: %w", sanitizeTransportError(err))
	}

	defer resp.Body.Close() //nolint:errcheck // response body close errors are informational in defer

	var result getFileResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding getFile response: %w", err)
	}

	if !result.OK || result.Result.FilePath == "" {
		return "", errors.New("telegram getFile returned no file path")
	}

	return result.Result.FilePath, nil
}

// sanitizeTransportError strips the bot-token-bearing request URL from an
// HTTP transport failure before it is ever wrapped, stored, or logged.
// http.Client.Do returns *url.Error on a transport-level failure (dial,
// timeout, TLS, ...), whose Error() string embeds the full failing request
// URL — here, ".../bot<TOKEN>/sendMessage" — which would otherwise leak the
// bot token into a dashboard-visible binding_error or an application log.
func sanitizeTransportError(err error) error {
	var urlErr *url.Error

	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s: %w", urlErr.Op, urlErr.Err)
	}

	return err
}
