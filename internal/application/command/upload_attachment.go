// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/errs"
)

// UploadAttachmentCommand uploads one file/image to a conversation. The bytes
// go to blob storage; a row is created with no message link yet (the caller
// references the returned id in the next ask/follow_up to attach it).
type UploadAttachmentCommand struct {
	ConversationID   uuid.UUID
	UploaderMemberID uuid.UUID
	Filename         string
	MimeType         string
	// Content is the raw bytes. Size is validated against the server cap by the
	// transport before this runs.
	Content []byte
}

// UploadAttachmentResult carries the new attachment id.
type UploadAttachmentResult struct {
	AttachmentID uuid.UUID
}

// attachmentConvReader resolves the conversation to scope the upload and check
// membership. *conversation.Store satisfies it.
type attachmentConvReader interface {
	ConversationByID(ctx context.Context, id uuid.UUID) (model.Conversation, error)
}

// attachmentWriter persists the attachment row. *conversation.AttachmentStore
// satisfies it.
type attachmentWriter interface {
	Create(ctx context.Context, a model.Attachment) error
}

// UploadAttachmentHandler handles UploadAttachmentCommand.
type UploadAttachmentHandler struct {
	convRepo     attachmentConvReader
	participants participantStore
	candidacy    candidacyStore
	attachments  attachmentWriter
	blobs        service.BlobStore
}

// MustNewUploadAttachmentHandler builds a handler. It panics on nil
// dependencies.
func MustNewUploadAttachmentHandler(
	convRepo attachmentConvReader,
	participants participantStore,
	candidacy candidacyStore,
	attachments attachmentWriter,
	blobs service.BlobStore,
) UploadAttachmentHandler {
	if convRepo == nil || participants == nil || candidacy == nil || attachments == nil || blobs == nil {
		panic("UploadAttachmentHandler requires non-nil convRepo, participants, candidacy, attachments, and blobs")
	}

	return UploadAttachmentHandler{
		convRepo:     convRepo,
		participants: participants,
		candidacy:    candidacy,
		attachments:  attachments,
		blobs:        blobs,
	}
}

// Handle validates the caller is on the conversation, stores the bytes in the
// blob store, and records the attachment (unlinked). Returns
// errs.InvalidError when storage is disabled or the conversation is closed,
// errs.ForbiddenError
// when the caller is not on the conversation.
func (h UploadAttachmentHandler) Handle(ctx context.Context, cmd UploadAttachmentCommand) (UploadAttachmentResult, error) {
	if !h.blobs.Enabled() {
		return UploadAttachmentResult{}, errs.InvalidError{
			Field:  "attachment",
			Reason: "attachments are not available (object storage is not configured on this server)",
		}
	}

	conv, err := h.convRepo.ConversationByID(ctx, cmd.ConversationID)
	if err != nil {
		return UploadAttachmentResult{}, translateErr(err, "conversation")
	}

	if conv.Status != model.ConversationStatusOpen && conv.Status != model.ConversationStatusAnswered {
		return UploadAttachmentResult{}, errs.InvalidError{Field: fieldConversationID, Reason: "conversation is closed"}
	}

	if err := memberOnConversation(ctx, conv, cmd.UploaderMemberID, h.participants, h.candidacy, "upload to this conversation"); err != nil {
		return UploadAttachmentResult{}, err
	}

	attachmentID := uuid.New()
	key := model.StorageKeyFor(conv.ProjectID, conv.ID, attachmentID, cmd.Filename)

	if err := h.blobs.Put(ctx, key, bytes.NewReader(cmd.Content), cmd.MimeType, int64(len(cmd.Content))); err != nil {
		return UploadAttachmentResult{}, errs.InternalError{Err: fmt.Errorf("storing attachment bytes: %w", err)}
	}

	att := model.Attachment{
		ID:                 attachmentID,
		ProjectID:          conv.ProjectID,
		ConversationID:     conv.ID,
		UploadedByMemberID: cmd.UploaderMemberID,
		Filename:           cmd.Filename,
		MimeType:           cmd.MimeType,
		SizeBytes:          int64(len(cmd.Content)),
		StorageKey:         key,
	}

	if err := h.attachments.Create(ctx, att); err != nil {
		// The blob is now orphaned; best-effort cleanup so a failed row does not
		// leave bytes behind.
		if delErr := h.blobs.Delete(ctx, key); delErr != nil {
			slog.WarnContext(ctx, "upload_attachment: cleaning orphaned blob after row failure",
				slog.String("key", key), slog.Any("error", delErr))
		}

		return UploadAttachmentResult{}, translateErr(err, "attachment")
	}

	return UploadAttachmentResult{AttachmentID: attachmentID}, nil
}
