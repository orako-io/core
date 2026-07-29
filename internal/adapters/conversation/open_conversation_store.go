// SPDX-License-Identifier: AGPL-3.0-or-later

package conversation

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/pgconv"
	postgres "github.com/orako-io/core/internal/pkg/postgres"
)

// OpenConversationStore implements the command.conversationOpener port. It writes
// a conversation row, its initial question message, and — for pool dispatches —
// the candidate set in a single transaction, all within the conversation
// bounded context.
type OpenConversationStore struct {
	pool *pgxpool.Pool
}

// NewOpenConversationStore builds an OpenConversationStore backed by pool.
func NewOpenConversationStore(pool *pgxpool.Pool) *OpenConversationStore {
	return &OpenConversationStore{pool: pool}
}

// OpenConversation atomically inserts the conversation, its initial message,
// and — for pool dispatches — the candidate set, so an unclaimed conversation
// can never exist without its pool. candidates is empty for direct asks.
// Returns adaptererr.ErrDuplicate when either ID already exists.
func (s *OpenConversationStore) OpenConversation(ctx context.Context, conv model.Conversation, msg model.Message, candidates []uuid.UUID) error {
	return postgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)

		if _, err := q.createConversation(ctx, newCreateConversationParams(conv)); err != nil {
			return fmt.Errorf("creating conversation: %w", adaptererr.Decode(err))
		}

		if _, err := q.addMessage(ctx, addMessageParams{
			ID:             msg.ID,
			ConversationID: msg.ConversationID,
			AuthorMemberID: pgconv.UUIDOrNull(msg.AuthorMemberID),
			Role:           msg.Role.String(),
			Body:           msg.Body,
			Source:         string(msg.Source),
			AgentClient:    msg.AgentClient,
		}); err != nil {
			return fmt.Errorf("adding message: %w", adaptererr.Decode(err))
		}

		for _, memberID := range candidates {
			if err := q.addConversationCandidate(ctx, addConversationCandidateParams{
				ConversationID: conv.ID,
				MemberID:       memberID,
			}); err != nil {
				return fmt.Errorf("adding candidate: %w", adaptererr.Decode(err))
			}
		}

		return nil
	})
}
