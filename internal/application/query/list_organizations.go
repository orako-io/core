// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"

	"github.com/google/uuid"
)

// ListOrganizationsQuery lists the organizations the caller participates in.
// CurrentOrgID flags the caller's active org in the result.
type ListOrganizationsQuery struct {
	// AccountID is the primary key of multi-org identity: every org the
	// account reaches (via any of its per-org member rows, or an org_members
	// admin row) is listed. Nil falls back to the member-keyed lookup (dev
	// stub, no account).
	AccountID uuid.UUID `exhaustruct:"optional"`
	// MemberID is the member-keyed fallback.
	MemberID     uuid.UUID
	CurrentOrgID uuid.UUID
}

// OrganizationView is a single organization the caller can switch to.
type OrganizationView struct {
	ID        uuid.UUID
	Name      string
	IsCurrent bool
}

// organizationsByMemberReader is the read port. *identity.OrganizationStore
// satisfies it.
type organizationsByMemberReader interface {
	OrganizationsByAccount(ctx context.Context, accountID uuid.UUID) ([]OrgSummary, error)
	OrganizationsByMember(ctx context.Context, memberID uuid.UUID) ([]OrgSummary, error)
}

// ListOrganizationsHandler handles ListOrganizationsQuery.
type ListOrganizationsHandler struct {
	reader organizationsByMemberReader
}

// MustNewListOrganizationsHandler builds a handler. It panics on a nil
// dependency.
func MustNewListOrganizationsHandler(reader organizationsByMemberReader) ListOrganizationsHandler {
	if reader == nil {
		panic("ListOrganizationsHandler requires a non-nil organizationsByMemberReader")
	}

	return ListOrganizationsHandler{reader: reader}
}

// Handle returns the caller's organizations with the active one flagged.
func (h ListOrganizationsHandler) Handle(ctx context.Context, q ListOrganizationsQuery) ([]OrganizationView, error) {
	orgs, err := h.listOrgs(ctx, q)
	if err != nil {
		return nil, err
	}

	views := make([]OrganizationView, len(orgs))
	for i, o := range orgs {
		views[i] = OrganizationView{ID: o.ID, Name: o.Name, IsCurrent: o.ID == q.CurrentOrgID}
	}

	return views, nil
}

// listOrgs prefers the account-keyed lookup (sees every per-org member row and
// org_members admin rows); the member-keyed path serves account-less callers
// (dev stub).
func (h ListOrganizationsHandler) listOrgs(ctx context.Context, q ListOrganizationsQuery) ([]OrgSummary, error) {
	if q.AccountID != uuid.Nil {
		return h.reader.OrganizationsByAccount(ctx, q.AccountID)
	}

	return h.reader.OrganizationsByMember(ctx, q.MemberID)
}
