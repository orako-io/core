// SPDX-License-Identifier: AGPL-3.0-or-later

package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// ConversationRepository is the write-side port for conversation and message
// persistence. Driven adapters under internal/adapters implement it; the domain
// owns the contract.
//
// Implementations expose only the sentinel errors from
// internal/adapters/errors, never raw driver errors.
type ConversationRepository interface {
	// CreateConversation durably stores a new conversation. Returns
	// adaptererr.ErrDuplicate when a conversation with the same ID exists.
	CreateConversation(ctx context.Context, conv model.Conversation) error
	// ConversationByID fetches a conversation by ID. Returns
	// adaptererr.ErrNotFound when no conversation with that ID exists.
	ConversationByID(ctx context.Context, id uuid.UUID) (model.Conversation, error)
	// ConversationBySlackThread fetches a conversation by its Slack thread
	// correlation keys. Returns adaptererr.ErrNotFound when absent.
	ConversationBySlackThread(ctx context.Context, channelID, threadTS string) (model.Conversation, error)
	// ConversationByTelegramMessage fetches a conversation by its Telegram
	// correlation keys (chat_id + message_id of the bot's outbound message).
	// Returns adaptererr.ErrNotFound when absent.
	ConversationByTelegramMessage(ctx context.Context, chatID, messageID string) (model.Conversation, error)
	// UpdateStatus persists a status change on an existing conversation and
	// bumps updated_at. Returns adaptererr.ErrNotFound when absent.
	UpdateStatus(ctx context.Context, id uuid.UUID, status model.ConversationStatus) error
	// UpdateMetadata persists the agent-curated history metadata (summary,
	// tags, entities), bumps updated_at, and returns the refreshed aggregate.
	// Callers normalize the inputs (model.NormalizeSummary / NormalizeTags)
	// first. Returns adaptererr.ErrNotFound when the conversation is absent.
	UpdateMetadata(ctx context.Context, id uuid.UUID, summary string, tags, entities []string) (model.Conversation, error)
	// AddMessage appends a message to a conversation. Returns
	// adaptererr.ErrDuplicate when a message with the same ID already exists.
	AddMessage(ctx context.Context, msg model.Message) error
	// MessagesByConversation returns all messages for a conversation in
	// chronological (created_at ASC) order.
	MessagesByConversation(ctx context.Context, conversationID uuid.UUID) ([]model.Message, error)
}
