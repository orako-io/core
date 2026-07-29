// SPDX-License-Identifier: AGPL-3.0-or-later

package conversation

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	postgres "github.com/orako-io/core/internal/pkg/postgres"
)

// SurfaceStore persists conversation surfaces (hub-and-spoke phase 4): the
// native platform threads a conversation fans out to, and which members each
// covers.
type SurfaceStore struct {
	pool *pgxpool.Pool
}

// NewSurfaceStore builds a SurfaceStore backed by pool.
func NewSurfaceStore(pool *pgxpool.Pool) *SurfaceStore {
	return &SurfaceStore{pool: pool}
}

// Create inserts the surface; created=false means another writer already holds
// the (conversation, provider) slot — the caller re-reads the winner.
func (s *SurfaceStore) Create(ctx context.Context, surface model.ConversationSurface) (created bool, err error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).createConversationSurface(ctx, createConversationSurfaceParams{
		ID:               surface.ID,
		ConversationID:   surface.ConversationID,
		Provider:         surface.Provider,
		Kind:             surface.Kind,
		ChannelID:        surface.ChannelID,
		ThreadID:         surface.ThreadID,
		CoveredMemberIds: surface.CoveredMemberIDs,
	})
	if err != nil {
		return false, fmt.Errorf("creating conversation surface: %w", adaptererr.Decode(err))
	}

	return rows > 0, nil
}

// ByConversationProvider resolves the conversation's surface on one platform.
// Returns adaptererr.ErrNotFound (decoded) when none exists.
func (s *SurfaceStore) ByConversationProvider(ctx context.Context, conversationID uuid.UUID, provider string) (model.ConversationSurface, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).surfaceByConversationProvider(ctx, surfaceByConversationProviderParams{
		ConversationID: conversationID,
		Provider:       provider,
	})
	if err != nil {
		return model.ConversationSurface{}, fmt.Errorf("resolving surface by conversation: %w", adaptererr.Decode(err))
	}

	return surfaceRowToModel(row), nil
}

// ByThread resolves the conversation surface a platform thread belongs to —
// the inbound correlation for a message typed in the thread.
func (s *SurfaceStore) ByThread(ctx context.Context, provider, threadID string) (model.ConversationSurface, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).surfaceByThread(ctx, surfaceByThreadParams{
		Provider: provider,
		ThreadID: threadID,
	})
	if err != nil {
		return model.ConversationSurface{}, fmt.Errorf("resolving surface by thread: %w", adaptererr.Decode(err))
	}

	return surfaceRowToModel(row), nil
}

// ByChannelThread resolves the surface a platform reply belongs to by its full
// (provider, channel_id, thread_id) key — the inbound correlation for Slack and
// Telegram, whose thread_id (thread_ts / message_id) is unique only within a
// channel/chat, not globally. Returns adaptererr.ErrNotFound (decoded) when none
// matches.
func (s *SurfaceStore) ByChannelThread(ctx context.Context, provider, channelID, threadID string) (model.ConversationSurface, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).surfaceByChannelThread(ctx, surfaceByChannelThreadParams{
		Provider:  provider,
		ChannelID: channelID,
		ThreadID:  threadID,
	})
	if err != nil {
		return model.ConversationSurface{}, fmt.Errorf("resolving surface by channel thread: %w", adaptererr.Decode(err))
	}

	return surfaceRowToModel(row), nil
}

// AddCoveredMember appends memberID to the surface's covered set (idempotent).
func (s *SurfaceStore) AddCoveredMember(ctx context.Context, surfaceID, memberID uuid.UUID) error {
	if err := New(postgres.Conn(ctx, s.pool)).addSurfaceCoveredMember(ctx, addSurfaceCoveredMemberParams{
		ID:      surfaceID,
		Column2: memberID,
	}); err != nil {
		return fmt.Errorf("adding covered member: %w", adaptererr.Decode(err))
	}

	return nil
}

// Archive stamps archived_at once (CAS); archived=false means a concurrent
// close already archived it.
func (s *SurfaceStore) Archive(ctx context.Context, surfaceID uuid.UUID) (archived bool, err error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).markSurfaceArchived(ctx, surfaceID)
	if err != nil {
		return false, fmt.Errorf("archiving surface: %w", adaptererr.Decode(err))
	}

	return rows > 0, nil
}

// WebhookForChannel resolves the cached Discord webhook pair for a channel.
// Returns adaptererr.ErrNotFound (decoded) when none is cached yet.
func (s *SurfaceStore) WebhookForChannel(ctx context.Context, channelID string) (webhookID, token string, err error) {
	row, err := New(postgres.Conn(ctx, s.pool)).getChannelWebhook(ctx, channelID)
	if err != nil {
		return "", "", fmt.Errorf("resolving channel webhook: %w", adaptererr.Decode(err))
	}

	return row.WebhookID, row.WebhookToken, nil
}

// SaveChannelWebhook caches a freshly created webhook pair for a channel
// (last writer wins).
func (s *SurfaceStore) SaveChannelWebhook(ctx context.Context, channelID, webhookID, token string) error {
	if err := New(postgres.Conn(ctx, s.pool)).upsertChannelWebhook(ctx, upsertChannelWebhookParams{
		ChannelID:    channelID,
		WebhookID:    webhookID,
		WebhookToken: token,
	}); err != nil {
		return fmt.Errorf("caching channel webhook: %w", adaptererr.Decode(err))
	}

	return nil
}

// surfaceRowToModel maps the generated row to the domain model.
func surfaceRowToModel(row ConversationSurface) model.ConversationSurface {
	surface := model.ConversationSurface{
		ID:               row.ID,
		ConversationID:   row.ConversationID,
		Provider:         row.Provider,
		Kind:             row.Kind,
		ChannelID:        row.ChannelID,
		ThreadID:         row.ThreadID,
		CoveredMemberIDs: row.CoveredMemberIds,
		CreatedAt:        row.CreatedAt,
	}

	if row.ArchivedAt.Valid {
		t := row.ArchivedAt.Time
		surface.ArchivedAt = &t
	}

	return surface
}
