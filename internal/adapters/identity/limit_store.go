// SPDX-License-Identifier: AGPL-3.0-or-later

package identity

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/orako-io/core/internal/pkg/postgres"
)

// MemberLimitStore serializes and counts member limit checks.
type MemberLimitStore struct {
	pool *pgxpool.Pool
}

// NewMemberLimitStore builds the member and seat limit adapter.
func NewMemberLimitStore(pool *pgxpool.Pool) *MemberLimitStore {
	return &MemberLimitStore{pool: pool}
}

// CountByOrg returns the active member count for an organization.
func (s *MemberLimitStore) CountByOrg(ctx context.Context, orgID uuid.UUID) (int, error) {
	return NewOrganizationStore(s.pool).CountByOrg(ctx, orgID)
}

// CountInstanceSeats returns the instance-wide seat count.
func (s *MemberLimitStore) CountInstanceSeats(ctx context.Context) (int, error) {
	return NewOrganizationStore(s.pool).CountInstanceSeats(ctx)
}

// LockOrg serializes org-scoped limit checks in the current transaction.
func (s *MemberLimitStore) LockOrg(ctx context.Context, orgID uuid.UUID) error {
	return lockLimit(ctx, s.pool, "org:"+orgID.String())
}

// LockInstance serializes instance-scoped limit checks in the current transaction.
func (s *MemberLimitStore) LockInstance(ctx context.Context) error {
	return lockLimit(ctx, s.pool, "instance")
}

// OrganizationLimitStore serializes and counts organization limit checks.
type OrganizationLimitStore struct {
	pool *pgxpool.Pool
}

// NewOrganizationLimitStore builds the organization limit adapter.
func NewOrganizationLimitStore(pool *pgxpool.Pool) *OrganizationLimitStore {
	return &OrganizationLimitStore{pool: pool}
}

// CountAll returns the number of organizations.
func (s *OrganizationLimitStore) CountAll(ctx context.Context) (int, error) {
	return NewOrganizationStore(s.pool).CountAll(ctx)
}

// LockInstance serializes instance-scoped limit checks in the current transaction.
func (s *OrganizationLimitStore) LockInstance(ctx context.Context) error {
	return lockLimit(ctx, s.pool, "instance")
}

// ProjectLimitStore serializes and counts project limit checks.
type ProjectLimitStore struct {
	pool *pgxpool.Pool
}

// NewProjectLimitStore builds the project limit adapter.
func NewProjectLimitStore(pool *pgxpool.Pool) *ProjectLimitStore {
	return &ProjectLimitStore{pool: pool}
}

// CountByOrg returns the project count for an organization.
func (s *ProjectLimitStore) CountByOrg(ctx context.Context, orgID uuid.UUID) (int, error) {
	return NewProjectStore(s.pool).CountByOrg(ctx, orgID)
}

// LockOrg serializes org-scoped limit checks in the current transaction.
func (s *ProjectLimitStore) LockOrg(ctx context.Context, orgID uuid.UUID) error {
	return lockLimit(ctx, s.pool, "org:"+orgID.String())
}

func lockLimit(ctx context.Context, pool *pgxpool.Pool, scope string) error {
	if _, err := postgres.Conn(ctx, pool).Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "orako:limit:"+scope); err != nil {
		return fmt.Errorf("locking resource limit %s: %w", scope, err)
	}

	return nil
}
