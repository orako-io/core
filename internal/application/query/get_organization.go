// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"

	"github.com/google/uuid"
)

// GetOrganizationQuery reads the caller's organization identity.
type GetOrganizationQuery struct {
	OrgID uuid.UUID
}

// OrgIdentityView is the organization's identity surface for the dashboard
// (the workspace slug is derived from the name client-side).
type OrgIdentityView struct {
	ID   uuid.UUID
	Name string
}

// organizationReader is the read port. *identity.OrganizationStore satisfies it.
type organizationReader interface {
	ReadOrgIdentity(ctx context.Context, id uuid.UUID) (OrgIdentityView, error)
}

// GetOrganizationHandler handles GetOrganizationQuery.
type GetOrganizationHandler struct {
	reader organizationReader
}

// MustNewGetOrganizationHandler builds a handler. It panics on a nil
// dependency.
func MustNewGetOrganizationHandler(reader organizationReader) GetOrganizationHandler {
	if reader == nil {
		panic("GetOrganizationHandler requires a non-nil organizationReader")
	}

	return GetOrganizationHandler{reader: reader}
}

// Handle returns the caller's organization identity (id + name).
func (h GetOrganizationHandler) Handle(ctx context.Context, q GetOrganizationQuery) (OrgIdentityView, error) {
	org, err := h.reader.ReadOrgIdentity(ctx, q.OrgID)
	if err != nil {
		return OrgIdentityView{}, translateReadError(err, "organization")
	}

	return org, nil
}
