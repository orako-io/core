// SPDX-License-Identifier: AGPL-3.0-or-later

package conversation_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/conversation"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// fakeBlobDeleter records the keys DeleteConversation asks it to remove and
// reports enabled per its flag. Satisfies conversation.BlobDeleter.
type fakeBlobDeleter struct {
	enabled bool
	deleted []string
	err     error
}

func (f *fakeBlobDeleter) Delete(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)

	return f.err
}

func (f *fakeBlobDeleter) Enabled() bool { return f.enabled }

// TestDeleteConversationRemovesAttachmentBlobs proves the storage-leak fix: a
// hard delete enumerates the conversation's attachment storage keys and asks the
// blob store to remove each blob's bytes (the metadata rows cascade with the
// conversation; the bytes have no FK and must be deleted explicitly).
func TestDeleteConversationRemovesAttachmentBlobs(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	blobs := &fakeBlobDeleter{enabled: true}
	store := conversation.NewStore(pool).WithBlobDeleter(blobs)

	projectID := testsupport.SeedProject(t, pool)
	memberID := testsupport.SeedMember(t, pool)
	convID := uuid.New()

	conv, err := model.NewConversation(convID, projectID, memberID, "has attachments?")
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}

	if err := store.CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	keyOne := "attachments/" + convID.String() + "/one.png"
	keyTwo := "attachments/" + convID.String() + "/two.pdf"

	for _, k := range []string{keyOne, keyTwo} {
		if _, err := pool.Exec(t.Context(),
			`INSERT INTO attachments (id, project_id, conversation_id, filename, mime_type, size_bytes, storage_key)
			 VALUES ($1, $2, $3, 'f', 'application/octet-stream', 10, $4)`,
			uuid.New(), projectID, convID, k,
		); err != nil {
			t.Fatalf("seeding attachment: %v", err)
		}
	}

	if err := store.DeleteConversation(t.Context(), convID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}

	if len(blobs.deleted) != 2 {
		t.Fatalf("want 2 blobs deleted, got %d (%v)", len(blobs.deleted), blobs.deleted)
	}

	got := map[string]bool{}
	for _, k := range blobs.deleted {
		got[k] = true
	}

	if !got[keyOne] || !got[keyTwo] {
		t.Errorf("deleted keys = %v, want both %q and %q", blobs.deleted, keyOne, keyTwo)
	}
}

// TestDeleteConversationSkipsBlobsWhenDisabled proves a metadata-only deployment
// (blob store reporting !Enabled) still deletes the conversation and never calls
// Delete — the enumerate-then-delete step is gated on Enabled.
func TestDeleteConversationSkipsBlobsWhenDisabled(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	blobs := &fakeBlobDeleter{enabled: false}
	store := conversation.NewStore(pool).WithBlobDeleter(blobs)

	projectID := testsupport.SeedProject(t, pool)
	memberID := testsupport.SeedMember(t, pool)
	convID := uuid.New()

	conv, err := model.NewConversation(convID, projectID, memberID, "no object store")
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}

	if err := store.CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	if _, err := pool.Exec(t.Context(),
		`INSERT INTO attachments (id, project_id, conversation_id, filename, mime_type, size_bytes, storage_key)
		 VALUES ($1, $2, $3, 'f', 'application/octet-stream', 10, $4)`,
		uuid.New(), projectID, convID, "attachments/skip",
	); err != nil {
		t.Fatalf("seeding attachment: %v", err)
	}

	if err := store.DeleteConversation(t.Context(), convID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}

	if len(blobs.deleted) != 0 {
		t.Errorf("want no blob deletes when disabled, got %v", blobs.deleted)
	}
}
