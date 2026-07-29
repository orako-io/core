// SPDX-License-Identifier: AGPL-3.0-or-later

// Package pgconv holds the pgtype <-> Go value conversions shared by every
// Postgres adapter (nullable text, uuid, and timestamptz mapping). It lets each
// bounded-context adapter package map sqlc rows without duplicating the helpers.
package pgconv

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// TextOrNull converts a Go string to a pgtype.Text; an empty string maps to NULL.
func TextOrNull(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{Valid: false}
	}

	return pgtype.Text{String: s, Valid: true}
}

// StringFromText converts a pgtype.Text to a Go string; NULL maps to "".
func StringFromText(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}

	return t.String
}

// UUIDOrNull converts a uuid.UUID to a pgtype.UUID; the nil UUID maps to NULL.
func UUIDOrNull(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{Valid: false}
	}

	return pgtype.UUID{Bytes: id, Valid: true}
}

// UUIDFromPgtype converts a pgtype.UUID to a uuid.UUID; NULL maps to uuid.Nil.
func UUIDFromPgtype(u pgtype.UUID) uuid.UUID {
	if !u.Valid {
		return uuid.Nil
	}

	return u.Bytes
}

// TimePtrFromTimestamptz converts a pgtype.Timestamptz to a *time.Time; NULL maps to nil.
func TimePtrFromTimestamptz(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}

	tm := t.Time

	return &tm
}

// NilOrgID is the all-zero UUID sentinel used in multi-project scope queries:
// an unresolved caller org disables the org filter (nil = every project).
const NilOrgID = "00000000-0000-0000-0000-000000000000"
