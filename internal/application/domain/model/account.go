// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// Account is an authenticated human — the login identity. One per person, above
// the org. It is distinct from Member (a project participant): a Member may have
// no Account (an external-only responder), and an Account participates in
// projects as a Member (linked via members.account_id).
type Account struct {
	ID uuid.UUID
	// Subject is the external IdP subject (the JWT "sub"). Empty for accounts
	// created via local auth without an external identity.
	Subject string `exhaustruct:"optional"`
	// Email is the verified login identity.
	Email string
	// DisplayName is the human's name.
	DisplayName string `exhaustruct:"optional"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewAccount builds an Account ready for first insertion, validating its
// invariants. Email is required (the login identity); Subject and DisplayName
// are optional.
func NewAccount(id uuid.UUID, email, subject, displayName string) (Account, error) {
	if id == uuid.Nil {
		return Account{}, errs.InvalidError{Field: "id", Reason: nilUUIDReason}
	}

	if strings.TrimSpace(email) == "" {
		return Account{}, errs.InvalidError{Field: "email", Reason: emptyReason}
	}

	now := time.Now().UTC()

	return Account{
		ID:          id,
		Subject:     subject,
		Email:       email,
		DisplayName: displayName,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}
