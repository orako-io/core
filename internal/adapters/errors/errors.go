// SPDX-License-Identifier: AGPL-3.0-or-later

// Package errors defines the sentinel errors driven adapters expose upward and
// the decoder that maps raw driver errors onto them.
//
// Adapters must never leak a pgx or pgconn error to their callers: every
// database failure is funneled through [Decode] so the application layer sees a
// small, stable set of sentinels it can classify with errors.Is, independent of
// the underlying driver.
package errors

import (
	stderrors "errors"
	"fmt"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The sentinel errors every adapter classifies failures into.
var (
	// ErrNotFound reports that a requested row does not exist.
	ErrNotFound = stderrors.New("not found")
	// ErrDuplicate reports a uniqueness violation (the row already exists).
	ErrDuplicate = stderrors.New("duplicate")
	// ErrInvalid reports that the write violated a schema constraint
	// (foreign key, not-null, or check).
	ErrInvalid = stderrors.New("invalid")
	// ErrConflict reports a concurrency conflict (serialization or deadlock)
	// that the caller may retry.
	ErrConflict = stderrors.New("conflict")
)

// Decode maps a raw driver error onto one of the package sentinels.
//
// It returns nil for a nil input, a sentinel for every recognized failure
// class, and an opaque wrapped error for anything unrecognized so the cause is
// never silently dropped. The sentinel is wrapped so errors.Is matches it while
// the original driver error stays available for logging via errors.Unwrap.
func Decode(err error) error {
	if err == nil {
		return nil
	}

	if stderrors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}

	var pgErr *pgconn.PgError
	if stderrors.As(err, &pgErr) {
		if pgErr.Code == pgerrcode.UniqueViolation {
			// Unique violations carry the violated constraint's name so the
			// caller can tell the user WHICH field is already taken ("Discord
			// user ID already in use"), not just "already exists".
			return &UniqueViolationError{Constraint: pgErr.ConstraintName, msg: pgErr.Message}
		}

		if sentinel := classify(pgErr.Code); sentinel != nil {
			return fmt.Errorf("%w: %s", sentinel, pgErr.Message)
		}
	}

	return fmt.Errorf("database error: %w", err)
}

// UniqueViolationError is the decoded form of a Postgres unique violation. It
// matches errors.Is(err, ErrDuplicate) — existing classification keeps working
// — while additionally exposing the violated constraint so upper layers can
// name the conflicting field.
type UniqueViolationError struct {
	// Constraint is the violated index/constraint name (e.g.
	// "idx_members_discord_user_id_unique").
	Constraint string
	msg        string
}

// Error implements the error interface.
func (e *UniqueViolationError) Error() string {
	return "duplicate: " + e.msg
}

// Is reports equivalence with the ErrDuplicate sentinel so errors.Is keeps
// classifying this as a duplicate.
func (e *UniqueViolationError) Is(target error) bool {
	return target == ErrDuplicate
}

// classify maps a Postgres SQLSTATE to a sentinel, or nil if unrecognized.
func classify(code string) error {
	switch code {
	case pgerrcode.UniqueViolation:
		return ErrDuplicate
	case pgerrcode.ForeignKeyViolation, pgerrcode.NotNullViolation, pgerrcode.CheckViolation:
		return ErrInvalid
	case pgerrcode.SerializationFailure, pgerrcode.DeadlockDetected:
		return ErrConflict
	default:
		return nil
	}
}
