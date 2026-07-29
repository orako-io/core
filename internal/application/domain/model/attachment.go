// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Attachment is one file or image carried by a conversation message. The bytes
// live in blob storage (S3-compatible); this is the metadata + the storage key
// that locates them. MessageID is nil until the attachment is linked to a
// message (an agent uploads first, then references the id when posting).
type Attachment struct {
	ID                 uuid.UUID
	ProjectID          uuid.UUID
	ConversationID     uuid.UUID
	MessageID          uuid.UUID `exhaustruct:"optional"` // nil until linked
	UploadedByMemberID uuid.UUID `exhaustruct:"optional"` // nil for system
	Filename           string
	MimeType           string
	SizeBytes          int64
	// StorageKey locates the bytes in the blob store, e.g.
	// "<project>/<conversation>/<attachment>/<filename>".
	StorageKey string
	CreatedAt  time.Time `exhaustruct:"optional"`
}

// StorageKeyFor builds the deterministic blob key for an attachment. The
// filename is sanitized so a crafted name (e.g. "../…", control chars) can't
// alter the key path (L3); the unique attachmentID prefix already isolates it.
func StorageKeyFor(projectID, conversationID, attachmentID uuid.UUID, filename string) string {
	return projectID.String() + "/" + conversationID.String() + "/" + attachmentID.String() + "/" + sanitizeFilename(filename)
}

// sanitizeFilename reduces filename to a safe basename: directory components are
// dropped and path separators / control chars are replaced. Downloads are
// presigned by exact key, so this suffix is cosmetic.
func sanitizeFilename(filename string) string {
	base := path.Base(filename)

	base = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return '_'
		}

		return r
	}, base)

	if base == "" || base == "." || base == ".." {
		return "file"
	}

	return base
}
