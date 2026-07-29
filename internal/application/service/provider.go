// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// ErrUnrecognizedMessage means an inbound payload does not map to a conversation.
var ErrUnrecognizedMessage = errors.New("unrecognized message")

// ErrNoProvider means a project has no messaging provider.
var ErrNoProvider = errors.New("no messaging provider configured for project")

// MessageKind classifies an outbound message.
type MessageKind string

// Outbound message kinds.
const (
	MessageKindQuestion MessageKind = "question"
	MessageKindNudge    MessageKind = "nudge"
	MessageKindHistory  MessageKind = "history"
	MessageKindRelay    MessageKind = "relay"
	MessageKindClosure  MessageKind = "closure"
)

// OutboundMessage is a provider-agnostic message.
type OutboundMessage struct {
	ProjectID         uuid.UUID
	ConversationID    uuid.UUID
	ResponderMemberID uuid.UUID
	RecipientMemberID uuid.UUID   `exhaustruct:"optional"`
	Kind              MessageKind `exhaustruct:"optional"`
	MirrorAuthor      string      `exhaustruct:"optional"`
	Question          string
	Context           string               `exhaustruct:"optional"`
	Attachments       []OutboundAttachment `exhaustruct:"optional"`
}

// OutboundAttachment is a file attached to an outbound message.
type OutboundAttachment struct {
	Filename  string
	MimeType  string
	SizeBytes int64
	URL       string
}

// IsImage reports whether the attachment is an image.
func (a OutboundAttachment) IsImage() bool {
	return strings.HasPrefix(a.MimeType, "image/")
}

// Recipient resolves the effective delivery recipient.
func (m OutboundMessage) Recipient() uuid.UUID {
	if m.RecipientMemberID != uuid.Nil {
		return m.RecipientMemberID
	}

	return m.ResponderMemberID
}

// FormatQuestion formats a question and optional context and answer link.
func FormatQuestion(question, context, convURL string) string {
	var b strings.Builder

	b.WriteString("[orako] ")
	b.WriteString(question)

	if context != "" {
		b.WriteString("\n\nContext: ")
		b.WriteString(context)
	}

	b.WriteString(answerFooter(convURL))

	return b.String()
}

func answerFooter(convURL string) string {
	if convURL == "" {
		return ""
	}

	return "\n\n↳ Answer this thread from your agent (get_conversation / orako_answer): " + convURL
}

// FormatOutbound formats an outbound message according to its kind.
func FormatOutbound(msg OutboundMessage, convURL string) string {
	switch msg.Kind {
	case MessageKindRelay:
		if msg.MirrorAuthor != "" {
			return "💬 " + msg.MirrorAuthor + ":\n" + msg.Question
		}

		return "💬 " + msg.Question
	case MessageKindHistory:
		return msg.Question + answerFooter(convURL)
	case MessageKindQuestion, MessageKindNudge, MessageKindClosure:
		return FormatQuestion(msg.Question, msg.Context, convURL)
	}

	return FormatQuestion(msg.Question, msg.Context, convURL)
}

// ConversationURL builds a dashboard link for a conversation.
func ConversationURL(dashboardBaseURL string, conversationID uuid.UUID) string {
	if dashboardBaseURL == "" {
		return ""
	}

	return strings.TrimRight(dashboardBaseURL, "/") + "/conversations/" + conversationID.String()
}

// SplitForDelivery splits text into provider-sized chunks.
func SplitForDelivery(text string, maxRunes int) []string {
	if maxRunes <= 0 || utf8.RuneCountInString(text) <= maxRunes {
		return []string{text}
	}

	var chunks []string

	remaining := text
	for utf8.RuneCountInString(remaining) > maxRunes {
		head, tail := splitOnce(remaining, maxRunes)
		if head == "" {
			runes := []rune(remaining)
			head, tail = string(runes[:maxRunes]), string(runes[maxRunes:])
		}

		chunks = append(chunks, head)
		remaining = tail
	}

	if strings.TrimSpace(remaining) != "" {
		chunks = append(chunks, remaining)
	}

	return chunks
}

func splitOnce(s string, maxRunes int) (head, tail string) {
	runes := []rune(s)
	window := string(runes[:maxRunes])

	cut, sepLen := -1, 0

	if i := strings.LastIndex(window, "\n\n"); i > 0 {
		cut, sepLen = i, 2
	} else if i := strings.LastIndex(window, "\n"); i > 0 {
		cut, sepLen = i, 1
	} else if i := lastSentenceEnd(window); i > 0 {
		cut, sepLen = i, 1
	} else if i := strings.LastIndex(window, " "); i > 0 {
		cut, sepLen = i, 1
	}

	if cut <= 0 {
		return window, string(runes[maxRunes:])
	}

	return strings.TrimRight(window[:cut], "\n "), strings.TrimLeft(s[cut+sepLen:], "\n ")
}

func lastSentenceEnd(window string) int {
	best := -1

	for _, sep := range []string{". ", "! ", "? "} {
		if i := strings.LastIndex(window, sep); i >= 0 && i+1 > best {
			best = i + 1 // cut after the punctuation; sepLen=1 eats the space
		}
	}

	return best
}

// SendSplit delivers chunks sequentially and returns the last message reference.
func SendSplit(ctx context.Context, chunks []string, send func(context.Context, string) (MessageRef, error)) (MessageRef, error) {
	var last MessageRef

	for i, chunk := range chunks {
		ref, err := send(ctx, chunk)
		if err != nil {
			if i == 0 {
				return MessageRef{}, err
			}

			slog.WarnContext(ctx, "delivery: chunk send failed past the first; keeping the delivered prefix",
				slog.Int("failed_chunk", i+1), slog.Int("total_chunks", len(chunks)), slog.Any("error", err))

			return last, nil
		}

		last = ref
	}

	return last, nil
}

// MemberDisplayNameReader resolves members by ID.
type MemberDisplayNameReader interface {
	ByID(ctx context.Context, id uuid.UUID) (model.Member, error)
}

// ResolveDisplayName resolves a member name with a neutral fallback.
func ResolveDisplayName(ctx context.Context, members MemberDisplayNameReader, id uuid.UUID) string {
	if id == uuid.Nil {
		return "another responder"
	}

	member, err := members.ByID(ctx, id)
	if err != nil || strings.TrimSpace(member.DisplayName) == "" {
		return "another responder"
	}

	return member.DisplayName
}

// MessageRef identifies a provider message.
type MessageRef struct {
	ChannelID string
	MessageID string
}

// InboundMessage is a normalized provider reply.
type InboundMessage struct {
	ConversationID uuid.UUID
	AuthorMemberID uuid.UUID
	Body           string
	ProjectID      uuid.UUID           `exhaustruct:"optional"`
	Attachments    []InboundAttachment `exhaustruct:"optional"`
}

// InboundAttachment is a provider-hosted attachment.
type InboundAttachment struct {
	Filename  string
	MimeType  string
	SizeBytes int64
	FetchURL  string
}

// Provider delivers questions and parses replies.
type Provider interface {
	Deliver(ctx context.Context, msg OutboundMessage) (MessageRef, error)
	ParseInbound(ctx context.Context, raw []byte) (InboundMessage, error)
}

// Editor rewrites delivered provider messages.
type Editor interface {
	Edit(ctx context.Context, ref MessageRef, text string) error
}

// ChannelPoster posts into provider channels.
type ChannelPoster interface {
	PostChannel(ctx context.Context, channelID, text string) (MessageRef, error)
}

// IdentityPoster posts into a channel under a visible identity.
type IdentityPoster interface {
	PostAsIdentity(ctx context.Context, parentChannelID, threadID, username, text string) error
}

// ChannelAttachmentPoster posts attachments into channels.
type ChannelAttachmentPoster interface {
	PostChannelWithFiles(ctx context.Context, channelID, text string, atts []OutboundAttachment) (MessageRef, error)
}

// IdentityAttachmentPoster posts attachments under a visible identity.
type IdentityAttachmentPoster interface {
	PostAsIdentityWithFiles(ctx context.Context, parentChannelID, threadID, username, text string, atts []OutboundAttachment) error
}

// ThreadSurfacer manages provider conversation threads.
type ThreadSurfacer interface {
	CreateThread(ctx context.Context, parentChannelID, name string) (threadID string, err error)
	AddThreadMember(ctx context.Context, threadID, platformUserID string) error
	ArchiveThread(ctx context.Context, threadID string) error
	DeleteThread(ctx context.Context, threadID string) error
}
