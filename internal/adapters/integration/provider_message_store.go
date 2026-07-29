// SPDX-License-Identifier: AGPL-3.0-or-later

package integration

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	postgres "github.com/orako-io/core/internal/pkg/postgres"
)

// ProviderMessageStore is the Postgres-backed delivery ledger: one row per
// (conversation, member) pool DM, keyed for idempotent re-delivery and
// inbound correlation by (channel, message ref).
type ProviderMessageStore struct {
	pool *pgxpool.Pool
}

// NewProviderMessageStore builds a ProviderMessageStore backed by pool.
func NewProviderMessageStore(pool *pgxpool.Pool) *ProviderMessageStore {
	return &ProviderMessageStore{pool: pool}
}

// Upsert inserts a ledger row for a (conversation, member) pair. A conflict
// on that pair is a pure no-op (ON CONFLICT DO NOTHING): the first
// successfully-recorded delivery for a candidate must never be overwritten by
// a later call, so a replayed CONVERSATION_OPENED (at-least-once redelivery)
// can never regress an existing row's state or clobber its channel/ref. The
// caller (delivery_notifier.go) is expected to check for an existing row
// before delivering in the first place; this is the storage-level backstop.
func (s *ProviderMessageStore) Upsert(ctx context.Context, msg model.ProviderMessage) error {
	id := msg.ID
	if id == uuid.Nil {
		id = uuid.New()
	}

	if _, err := New(postgres.Conn(ctx, s.pool)).upsertProviderMessage(ctx, upsertProviderMessageParams{
		ID:             id,
		ConversationID: msg.ConversationID,
		MemberID:       msg.MemberID,
		ProviderKind:   msg.ProviderKind,
		ChannelID:      msg.ChannelID,
		MessageRef:     msg.MessageRef,
		State:          string(msg.State),
	}); err != nil {
		return fmt.Errorf("upserting provider message: %w", adaptererr.Decode(err))
	}

	return nil
}

// ByConversation lists every ledger row for a conversation, oldest first.
func (s *ProviderMessageStore) ByConversation(ctx context.Context, conversationID uuid.UUID) ([]model.ProviderMessage, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).providerMessagesByConversation(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("listing provider messages: %w", adaptererr.Decode(err))
	}

	out := make([]model.ProviderMessage, len(rows))
	for i, row := range rows {
		out[i] = providerMessageRowToModel(row)
	}

	return out, nil
}

// ByProviderRef resolves the ledger row for a (channel, message ref) pair —
// the inbound correlation path when the direct conversations-thread lookup
// misses. Returns adaptererr.ErrNotFound when no row matches.
func (s *ProviderMessageStore) ByProviderRef(ctx context.Context, channelID, messageRef string) (model.ProviderMessage, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).providerMessageByChannelRef(ctx, providerMessageByChannelRefParams{
		ChannelID:  channelID,
		MessageRef: messageRef,
	})
	if err != nil {
		return model.ProviderMessage{}, fmt.Errorf("resolving provider message by ref: %w", adaptererr.Decode(err))
	}

	return providerMessageRowToModel(row), nil
}

// LatestByChannel resolves the most recently updated still-open ledger row
// for a channel — the inbound correlation path for providers with no stable
// per-message reference to key an exact ByProviderRef match (Microsoft Teams:
// every message in a 1:1 personal conversation shares one conversation id).
// Returns adaptererr.ErrNotFound when no open row matches.
func (s *ProviderMessageStore) LatestByChannel(ctx context.Context, channelID string) (model.ProviderMessage, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).providerMessageLatestByChannel(ctx, channelID)
	if err != nil {
		return model.ProviderMessage{}, fmt.Errorf("resolving latest provider message by channel: %w", adaptererr.Decode(err))
	}

	return providerMessageRowToModel(row), nil
}

// SetState transitions the ledger row's lifecycle state. Returns
// adaptererr.ErrNotFound when no row with that id exists.
func (s *ProviderMessageStore) SetState(ctx context.Context, id uuid.UUID, state model.ProviderMessageState) error {
	rows, err := New(postgres.Conn(ctx, s.pool)).setProviderMessageState(ctx, setProviderMessageStateParams{
		ID:    id,
		State: string(state),
	})
	if err != nil {
		return fmt.Errorf("setting provider message state: %w", adaptererr.Decode(err))
	}

	if rows == 0 {
		return fmt.Errorf("provider message %s: %w", id, adaptererr.ErrNotFound)
	}

	return nil
}

// Finalize closes the reserve→deliver→finalize window for a row previously
// written as "reserving" (see Upsert's caller, delivery_notifier.go): it
// records the channel/message ref Deliver actually returned and transitions
// the state in one write, guarded on the row still being "reserving". Returns
// adaptererr.ErrNotFound when no "reserving" row with that id exists (either
// the id is wrong, or the row already moved past reserving — this must never
// silently no-op a caller expecting its finalize to land).
func (s *ProviderMessageStore) Finalize(ctx context.Context, id uuid.UUID, channelID, messageRef string, state model.ProviderMessageState) error {
	rows, err := New(postgres.Conn(ctx, s.pool)).finalizeProviderMessage(ctx, finalizeProviderMessageParams{
		ID:         id,
		ChannelID:  channelID,
		MessageRef: messageRef,
		State:      string(state),
	})
	if err != nil {
		return fmt.Errorf("finalizing provider message: %w", adaptererr.Decode(err))
	}

	if rows == 0 {
		return fmt.Errorf("provider message %s (reserving): %w", id, adaptererr.ErrNotFound)
	}

	return nil
}

// providerMessageRowToModel maps a generated ProviderMessage row to the
// domain model.
func providerMessageRowToModel(r ProviderMessage) model.ProviderMessage {
	return model.ProviderMessage{
		ID:             r.ID,
		ConversationID: r.ConversationID,
		MemberID:       r.MemberID,
		ProviderKind:   r.ProviderKind,
		ChannelID:      r.ChannelID,
		MessageRef:     r.MessageRef,
		State:          model.ProviderMessageState(r.State),
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
}
