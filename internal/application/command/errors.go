// SPDX-License-Identifier: AGPL-3.0-or-later

// Package command declares the CQRS write-side handlers.
//
// Each handler owns one write-side use case and translates adapter errors into
// application errors before returning.
package command

import (
	"errors"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/pkg/errs"
)

const (
	reasonNilUUID       = "must not be the nil UUID"
	reasonEmpty         = "must not be empty"
	fieldOrgID          = "org_id"
	fieldConversationID = "conversation_id"
	fieldMemberID       = "member_id"
	reasonNoOrgResolved = "no organization resolved for the caller"
)

// duplicateFieldByConstraint maps a violated unique constraint to the
// human-readable field it protects, so a duplicate error can say WHICH
// attribute is already taken instead of a bare "already exists: member".
var duplicateFieldByConstraint = map[string]string{ //nolint:gochecknoglobals // compile-time constant map; package-level by design
	"idx_members_discord_user_id_unique":  "Discord user ID",
	"idx_members_teams_user_id_unique":    "Teams user ID",
	"idx_members_telegram_chat_id_unique": "Telegram chat ID",
	"idx_accounts_email":                  "email",
}

// translateErr maps adapter sentinel errors to domain error types.
// The caller supplies a context label (e.g. the field or resource name) so
// the translated error carries meaningful detail.
func translateErr(err error, resource string) error {
	if errors.Is(err, adaptererr.ErrDuplicate) {
		dup := errs.DuplicateError{Resource: resource}

		var unique *adaptererr.UniqueViolationError
		if errors.As(err, &unique) {
			dup.Field = duplicateFieldByConstraint[unique.Constraint]
		}

		return dup
	}

	if errors.Is(err, adaptererr.ErrInvalid) {
		return errs.InvalidError{Field: resource, Reason: "constraint violation"}
	}

	if errors.Is(err, adaptererr.ErrNotFound) {
		return errs.NotFoundError{Resource: resource}
	}

	if errors.Is(err, adaptererr.ErrConflict) {
		return errs.ConflictError{}
	}

	return errs.InternalError{Err: err}
}
