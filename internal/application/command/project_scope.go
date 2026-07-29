// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// verifyProjectInOrg proves project belongs to callerOrgID before a mutating
// project-management op (rename, archive, delete) runs against it. This must
// run BEFORE the mutation — a bare org-admin check keyed on project ID alone
// would let an org-A admin rename/archive/delete any project in the database
// by UUID (a cross-tenant IDOR the RPC-level admin gate alone does not close).
// A caller with no resolved org (callerOrgID == uuid.Nil) is rejected — fail
// closed rather than fail open on an unresolved tenant.
func verifyProjectInOrg(project model.Project, callerOrgID uuid.UUID) error {
	if callerOrgID == uuid.Nil || project.OrgID != callerOrgID {
		return errs.ForbiddenError{Action: "manage this project"}
	}

	return nil
}
