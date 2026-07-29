// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/orako-io/core/internal/adapters/identity"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/postgres"
)

// orgSeeder is the shared integration-test fixture for standing up an org with
// its global project. It composes the per-context store methods in one real
// transaction — mirroring the exact unit of work the CreateOrganization command
// performs in production via service.Transactor. org_provisioning_test.go
// asserts this composition writes all rows and rolls back atomically; the other
// identity tests use it only to seed a valid org.
type orgSeeder struct{ pool *pgxpool.Pool }

func newOrgSeeder(pool *pgxpool.Pool) orgSeeder { return orgSeeder{pool: pool} }

func (s orgSeeder) CreateOrganizationWithGlobalProject(
	ctx context.Context,
	org model.Organization,
	adminAccountID uuid.UUID,
	globalProject model.Project,
	creatorMember model.Member,
) error {
	orgs := identity.NewOrganizationStore(s.pool)
	projects := identity.NewProjectStore(s.pool)
	members := identity.NewMemberStore(s.pool)

	return postgres.NewTransactor(s.pool).WithTx(ctx, func(ctx context.Context) error {
		if err := orgs.Create(ctx, org); err != nil {
			return err
		}

		if err := orgs.AddMember(ctx, model.OrgMembership{OrgID: org.ID, AccountID: adminAccountID, Role: model.OrgRoleAdmin}); err != nil {
			return err
		}

		if err := projects.CreateInOrg(ctx, globalProject); err != nil {
			return err
		}

		if err := members.CreateWithAccount(ctx, creatorMember); err != nil {
			return err
		}

		if err := projects.AddMember(ctx, repository.ProjectMembership{
			ProjectID: globalProject.ID,
			MemberID:  creatorMember.ID,
			Role:      model.RoleUnspecified,
			Domains:   []string{},
		}); err != nil {
			return err
		}

		return nil
	})
}
