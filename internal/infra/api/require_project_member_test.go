// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// TestRequireProjectMember covers the authorization rule that lets an org admin
// reach any project in their own org while keeping every other caller pinned to
// their active project.
func TestRequireProjectMember(t *testing.T) {
	t.Parallel()

	orgA := uuid.New()
	orgB := uuid.New()
	active := uuid.New()
	other := uuid.New() // a second project in orgA
	foreign := uuid.New()

	resolver := &fakeProjectResolver{byID: map[uuid.UUID]model.Project{
		other:   {ID: other, OrgID: orgA},
		foreign: {ID: foreign, OrgID: orgB},
	}}
	srv := &Server{projects: resolver}

	tests := []struct {
		name     string
		caller   *CallerIdentity
		target   uuid.UUID
		wantCode connect.Code
		wantOK   bool
	}{
		{
			name:   "member reaches own active project",
			caller: &CallerIdentity{ProjectID: active, ProjectIDs: []uuid.UUID{active}, OrgID: orgA},
			target: active,
			wantOK: true,
		},
		{
			name:   "org admin reaches another project in same org",
			caller: &CallerIdentity{ProjectID: active, ProjectIDs: []uuid.UUID{active}, OrgID: orgA, IsOrgAdmin: true},
			target: other,
			wantOK: true,
		},
		{
			name:     "scoped org admin denied a project outside scope in same org",
			caller:   &CallerIdentity{ProjectID: active, ProjectIDs: []uuid.UUID{active}, OrgID: orgA, IsOrgAdmin: true, Scoped: true},
			target:   other,
			wantCode: connect.CodePermissionDenied,
		},
		{
			name:     "org admin denied a project in a different org",
			caller:   &CallerIdentity{ProjectID: active, ProjectIDs: []uuid.UUID{active}, OrgID: orgA, IsOrgAdmin: true},
			target:   foreign,
			wantCode: connect.CodePermissionDenied,
		},
		{
			name:     "non-admin denied a project that is not their active one",
			caller:   &CallerIdentity{ProjectID: active, ProjectIDs: []uuid.UUID{active}, OrgID: orgA},
			target:   other,
			wantCode: connect.CodePermissionDenied,
		},
		{
			name:     "org admin with nil org denied",
			caller:   &CallerIdentity{ProjectID: active, ProjectIDs: []uuid.UUID{active}, OrgID: uuid.Nil, IsOrgAdmin: true},
			target:   other,
			wantCode: connect.CodePermissionDenied,
		},
		{
			name:     "missing identity is unauthenticated",
			caller:   nil,
			target:   active,
			wantCode: connect.CodeUnauthenticated,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			if tc.caller != nil {
				ctx = withCaller(ctx, *tc.caller)
			}

			err := srv.requireProjectMember(ctx, tc.target)

			if tc.wantOK {
				if err != nil {
					t.Fatalf("want nil error, got %v", err)
				}

				return
			}

			if got := connect.CodeOf(err); got != tc.wantCode {
				t.Fatalf("want code %v, got %v (err=%v)", tc.wantCode, got, err)
			}
		})
	}
}
