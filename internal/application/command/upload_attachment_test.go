// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/errs"
)

// fakeBlobStore records Put/Delete and reports Enabled per its flag.
type fakeBlobStore struct {
	enabled bool
	puts    map[string][]byte
	deleted []string
	putErr  error
}

func newFakeBlobStore(enabled bool) *fakeBlobStore {
	return &fakeBlobStore{enabled: enabled, puts: map[string][]byte{}}
}

func (f *fakeBlobStore) Put(_ context.Context, key string, r io.Reader, _ string, _ int64) error {
	if f.putErr != nil {
		return f.putErr
	}

	b, _ := io.ReadAll(r)
	f.puts[key] = b

	return nil
}

func (f *fakeBlobStore) Get(context.Context, string) (io.ReadCloser, error) { return nil, nil }

func (f *fakeBlobStore) SignedGetURL(context.Context, string, time.Duration) (string, error) {
	return "https://signed/x", nil
}

func (f *fakeBlobStore) Delete(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)

	return nil
}

func (f *fakeBlobStore) Enabled() bool { return f.enabled }

// fakeAttachmentWriter records created rows and can fail.
type fakeAttachmentWriter struct {
	created   []model.Attachment
	createErr error
}

func (f *fakeAttachmentWriter) Create(_ context.Context, a model.Attachment) error {
	if f.createErr != nil {
		return f.createErr
	}

	f.created = append(f.created, a)

	return nil
}

func newUploadHandler(conv *fakeConversationRepository, blobs service.BlobStore, writer *fakeAttachmentWriter) UploadAttachmentHandler {
	labeler := newFakeLabeler(conv)

	return MustNewUploadAttachmentHandler(conv, &fakeParticipantStore{}, labeler, writer, blobs)
}

func seedUploadConv(repo *fakeConversationRepository, asker uuid.UUID) uuid.UUID {
	id := uuid.New()
	repo.conversations[id] = model.Conversation{
		ID: id, ProjectID: uuid.New(), AskerMemberID: asker,
		Status: model.ConversationStatusOpen,
	}

	return id
}

// TestUpload_StoresBytesAndRow proves a participant's upload stores the bytes
// and records an unlinked attachment row scoped to the conversation.
func TestUpload_StoresBytesAndRow(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	asker := uuid.New()
	convID := seedUploadConv(repo, asker)

	blobs := newFakeBlobStore(true)
	writer := &fakeAttachmentWriter{}
	h := newUploadHandler(repo, blobs, writer)

	res, err := h.Handle(t.Context(), UploadAttachmentCommand{
		ConversationID:   convID,
		UploaderMemberID: asker,
		Filename:         "diagram.png",
		MimeType:         "image/png",
		Content:          []byte("PNGBYTES"),
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(blobs.puts) != 1 || len(writer.created) != 1 {
		t.Fatalf("want one blob + one row, got puts=%d rows=%d", len(blobs.puts), len(writer.created))
	}

	row := writer.created[0]
	if row.ID != res.AttachmentID || row.ConversationID != convID || row.MessageID != uuid.Nil {
		t.Errorf("row = %+v, want id=%s conv=%s unlinked", row, res.AttachmentID, convID)
	}

	if row.Filename != "diagram.png" || row.SizeBytes != 8 {
		t.Errorf("row metadata = %q/%d, want diagram.png/8", row.Filename, row.SizeBytes)
	}
}

// TestUpload_DisabledStorageRejected proves an upload against a server with no
// object storage returns a clear error, not a panic.
func TestUpload_DisabledStorageRejected(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	asker := uuid.New()
	convID := seedUploadConv(repo, asker)

	h := newUploadHandler(repo, newFakeBlobStore(false), &fakeAttachmentWriter{})

	_, err := h.Handle(t.Context(), UploadAttachmentCommand{
		ConversationID: convID, UploaderMemberID: asker,
		Filename: "x.png", MimeType: "image/png", Content: []byte("x"),
	})

	var invalid errs.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("disabled storage must return InvalidError, got %v", err)
	}
}

// TestUpload_NonParticipantForbidden proves a stranger cannot upload.
func TestUpload_NonParticipantForbidden(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	convID := seedUploadConv(repo, uuid.New())

	h := newUploadHandler(repo, newFakeBlobStore(true), &fakeAttachmentWriter{})

	_, err := h.Handle(t.Context(), UploadAttachmentCommand{
		ConversationID: convID, UploaderMemberID: uuid.New(), // not on the conversation
		Filename: "x.png", MimeType: "image/png", Content: []byte("x"),
	})

	var forbidden errs.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("stranger upload must be forbidden, got %v", err)
	}
}

// TestUpload_RowFailureCleansBlob proves a failed row insert deletes the
// orphaned blob so bytes are not left behind.
func TestUpload_RowFailureCleansBlob(t *testing.T) {
	t.Parallel()

	repo := newFakeConvRepo()
	asker := uuid.New()
	convID := seedUploadConv(repo, asker)

	blobs := newFakeBlobStore(true)
	h := newUploadHandler(repo, blobs, &fakeAttachmentWriter{createErr: errors.New("db down")})

	_, err := h.Handle(t.Context(), UploadAttachmentCommand{
		ConversationID: convID, UploaderMemberID: asker,
		Filename: "x.png", MimeType: "image/png", Content: []byte("x"),
	})
	if err == nil {
		t.Fatal("expected an error when the row insert fails")
	}

	if len(blobs.deleted) != 1 {
		t.Errorf("orphaned blob must be cleaned up, deleted=%v", blobs.deleted)
	}
}
