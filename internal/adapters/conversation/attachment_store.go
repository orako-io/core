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

// AttachmentStore persists attachment metadata rows (the bytes live in the
// blob store). It is the DB half of the attachments feature.
type AttachmentStore struct {
	pool *pgxpool.Pool
}

// NewAttachmentStore builds an AttachmentStore backed by pool.
func NewAttachmentStore(pool *pgxpool.Pool) *AttachmentStore {
	return &AttachmentStore{pool: pool}
}

// Create inserts an attachment row (message_id nil until linked).
func (s *AttachmentStore) Create(ctx context.Context, a model.Attachment) error {
	if err := New(postgres.Conn(ctx, s.pool)).createAttachment(ctx, createAttachmentParams{
		ID:                 a.ID,
		ProjectID:          a.ProjectID,
		ConversationID:     a.ConversationID,
		MessageID:          pgconv.UUIDOrNull(a.MessageID),
		UploadedByMemberID: pgconv.UUIDOrNull(a.UploadedByMemberID),
		Filename:           a.Filename,
		MimeType:           a.MimeType,
		SizeBytes:          a.SizeBytes,
		StorageKey:         a.StorageKey,
	}); err != nil {
		return fmt.Errorf("creating attachment: %w", adaptererr.Decode(err))
	}

	return nil
}

// ByID resolves one attachment. Returns adaptererr.ErrNotFound when absent.
func (s *AttachmentStore) ByID(ctx context.Context, id uuid.UUID) (model.Attachment, error) {
	row, err := New(postgres.Conn(ctx, s.pool)).attachmentByID(ctx, id)
	if err != nil {
		return model.Attachment{}, fmt.Errorf("resolving attachment: %w", adaptererr.Decode(err))
	}

	return attachmentRowToModel(row), nil
}

// LinkToMessage links previously-uploaded, still-unlinked attachments to a
// message, scoped to the conversation. Returns the count linked so the caller
// can detect an id that did not belong (mismatch = fewer rows than requested).
func (s *AttachmentStore) LinkToMessage(ctx context.Context, conversationID, messageID uuid.UUID, attachmentIDs []uuid.UUID) (int64, error) {
	if len(attachmentIDs) == 0 {
		return 0, nil
	}

	n, err := New(postgres.Conn(ctx, s.pool)).linkAttachmentsToMessage(ctx, linkAttachmentsToMessageParams{
		MessageID:      pgconv.UUIDOrNull(messageID),
		ConversationID: conversationID,
		Column3:        attachmentIDs,
	})
	if err != nil {
		return 0, fmt.Errorf("linking attachments: %w", adaptererr.Decode(err))
	}

	return n, nil
}

// ByConversation lists a conversation's linked attachments, for the join into
// message reads (get_conversation, fan-out history).
func (s *AttachmentStore) ByConversation(ctx context.Context, conversationID uuid.UUID) ([]model.Attachment, error) {
	rows, err := New(postgres.Conn(ctx, s.pool)).attachmentsByConversation(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("listing attachments: %w", adaptererr.Decode(err))
	}

	out := make([]model.Attachment, len(rows))
	for i, row := range rows {
		out[i] = attachmentRowToModel(row)
	}

	return out, nil
}

// AttachmentsByConversation is the read-side projection of ByConversation: it
// returns query.Attachment read models (raw metadata + storage key) so the query
// handler can mint signed URLs without depending on the domain aggregate.
func (s *AttachmentStore) AttachmentsByConversation(ctx context.Context, conversationID uuid.UUID) ([]query.Attachment, error) {
	atts, err := s.ByConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}

	out := make([]query.Attachment, len(atts))
	for i, a := range atts {
		out[i] = query.Attachment{
			ID:         a.ID,
			MessageID:  a.MessageID,
			Filename:   a.Filename,
			MimeType:   a.MimeType,
			SizeBytes:  a.SizeBytes,
			StorageKey: a.StorageKey,
		}
	}

	return out, nil
}

// StorageKeysByConversation lists every blob key for a conversation, so the
// delete handler can remove the bytes when the conversation is hard-deleted.
func (s *AttachmentStore) StorageKeysByConversation(ctx context.Context, conversationID uuid.UUID) ([]string, error) {
	keys, err := New(postgres.Conn(ctx, s.pool)).storageKeysByConversation(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("listing storage keys: %w", adaptererr.Decode(err))
	}

	return keys, nil
}

// attachmentRowToModel maps the generated row to the domain model.
func attachmentRowToModel(row Attachment) model.Attachment {
	return model.Attachment{
		ID:                 row.ID,
		ProjectID:          row.ProjectID,
		ConversationID:     row.ConversationID,
		MessageID:          pgconv.UUIDFromPgtype(row.MessageID),
		UploadedByMemberID: pgconv.UUIDFromPgtype(row.UploadedByMemberID),
		Filename:           row.Filename,
		MimeType:           row.MimeType,
		SizeBytes:          row.SizeBytes,
		StorageKey:         row.StorageKey,
		CreatedAt:          row.CreatedAt,
	}
}
