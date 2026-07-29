// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// This file holds the read-side DTOs the readers hand back — shapes that exist
// only to serve queries. They live in the query package (not domain/repository)
// so the write-side contracts stay free of read concerns. They may embed a
// domain aggregate: reading an aggregate is legitimate, what the rule forbids is
// the read shape itself living in the domain.

// ConversationWithProject is a conversation read model paired with its project's
// resolved name — the row shape a multi-project conversations list needs.
type ConversationWithProject struct {
	ConversationRecord
	ProjectName string
}

// ProjectDetailRow is one row of the Projects tab: a project's identity,
// archived status, and aggregate counts. Returned by ProjectsDetailedByOrg.
type ProjectDetailRow struct {
	ID                uuid.UUID
	Name              string
	Archived          bool
	MemberCount       int
	ConversationCount int
	ResolvedCount     int
}

// OrgSummary is one organization the caller belongs to (id + name), for the
// org switcher and org list.
type OrgSummary struct {
	ID   uuid.UUID
	Name string
}

// Attachment is the read model for a conversation attachment: the raw metadata a
// read handler needs before minting a signed download URL. Distinct from
// AttachmentView (which carries the signed URL and drops the storage key).
type Attachment struct {
	ID         uuid.UUID
	MessageID  uuid.UUID
	Filename   string
	MimeType   string
	SizeBytes  int64
	StorageKey string
}

// ConversationRecord is the core conversation read model that get_conversation
// authorizes on and assembles the view from — the fields it needs without the
// messages/participants (fetched separately) and without the domain aggregate.
// Title is already the display title (falls back to a truncated question).
type ConversationRecord struct {
	ID                uuid.UUID
	ProjectID         uuid.UUID
	AskerMemberID     uuid.UUID
	ResponderMemberID uuid.UUID
	Status            model.ConversationStatus
	Question          string
	Title             string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
