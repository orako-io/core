// SPDX-License-Identifier: AGPL-3.0-or-later

package conversation

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/pkg/pgconv"
	postgres "github.com/orako-io/core/internal/pkg/postgres"
)

// CandidateStore persists the candidate pools of dispatched conversations and
// owns the responder-label compare-and-set (first responder, attribution
// only — hub-and-spoke phase 2).
type CandidateStore struct {
	pool *pgxpool.Pool
}

// NewCandidateStore builds a CandidateStore backed by pool.
func NewCandidateStore(pool *pgxpool.Pool) *CandidateStore {
	return &CandidateStore{pool: pool}
}

// MembersByDomains resolves the candidate set at dispatch time: active
// project members whose expertise intersects domains, excluding the asker.
func (s *CandidateStore) MembersByDomains(ctx context.Context, projectID uuid.UUID, domains []string, exclude uuid.UUID) ([]uuid.UUID, error) {
	ids, err := New(postgres.Conn(ctx, s.pool)).projectMembersByDomains(ctx, projectMembersByDomainsParams{
		ProjectID: projectID,
		Column2:   domains,
		MemberID:  exclude,
	})
	if err != nil {
		return nil, fmt.Errorf("resolving candidates by domains: %w", adaptererr.Decode(err))
	}

	return ids, nil
}

// ByConversation lists a conversation's candidates, excluded ones included.
func (s *CandidateStore) ByConversation(ctx context.Context, conversationID uuid.UUID) ([]model.Candidate, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).candidatesByConversation(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("listing candidates: %w", adaptererr.Decode(err))
	}

	candidates := make([]model.Candidate, 0, len(rows))

	for _, row := range rows {
		c := model.Candidate{MemberID: row.MemberID, InvitedAt: row.InvitedAt.Time}

		if row.ExcludedAt.Valid {
			t := row.ExcludedAt.Time
			c.ExcludedAt = &t
		}

		candidates = append(candidates, c)
	}

	return candidates, nil
}

// IsActiveCandidate reports whether the member is still an active (not
// excluded) contacted candidate — i.e. on the conversation's distribution
// list.
func (s *CandidateStore) IsActiveCandidate(ctx context.Context, conversationID, memberID uuid.UUID) (bool, error) {
	active, err := New(postgres.Conn(ctx, s.pool)).isActiveCandidate(ctx, isActiveCandidateParams{
		ConversationID: conversationID,
		MemberID:       memberID,
	})
	if err != nil {
		return false, fmt.Errorf("checking candidate: %w", adaptererr.Decode(err))
	}

	return active, nil
}

// RecordFirstResponder records who first answered, for answer attribution. It is a
// compare-and-set: only the first answerer is written (returns true), a later
// answer is a no-op (false). Not a claim and not a gate — anyone on the
// conversation can still reply.
func (s *CandidateStore) RecordFirstResponder(ctx context.Context, conversationID, memberID uuid.UUID) (bool, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).recordFirstResponder(ctx, recordFirstResponderParams{
		ID:                conversationID,
		ResponderMemberID: pgconv.UUIDOrNull(memberID),
	})
	if err != nil {
		return false, fmt.Errorf("recording first responder: %w", adaptererr.Decode(err))
	}

	return rows > 0, nil
}

// Exclude removes a candidate from the pool (withdrawal).
func (s *CandidateStore) Exclude(ctx context.Context, conversationID, memberID uuid.UUID) error {
	if err := New(postgres.Conn(ctx, s.pool)).excludeConversationCandidate(ctx, excludeConversationCandidateParams{
		ConversationID: conversationID,
		MemberID:       memberID,
	}); err != nil {
		return fmt.Errorf("excluding candidate: %w", adaptererr.Decode(err))
	}

	return nil
}

// OpenPoolFor lists the unanswered pool conversations the member was
// contacted on (inbox "awaiting an answer"), as query read models.
func (s *CandidateStore) OpenPoolFor(ctx context.Context, memberID uuid.UUID) ([]query.ConversationRecord, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).openPoolConversationsForMember(ctx, memberID)
	if err != nil {
		return nil, fmt.Errorf("listing open pool conversations: %w", adaptererr.Decode(err))
	}

	conversations := make([]model.Conversation, len(rows))
	for i, row := range rows {
		conversations[i] = conversationRowToModel(conversationByIDRow(row))
	}

	return conversationsToRecords(conversations), nil
}
