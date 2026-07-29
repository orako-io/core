// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// fixedIdentityAuth is an Authenticator that always returns the configured
// CallerIdentity, allowing tests to inject arbitrary identities without the
// dev-stub format restrictions.
type fixedIdentityAuth struct {
	identity CallerIdentity
}

func (f fixedIdentityAuth) Authenticate(_ context.Context, _ string) (CallerIdentity, error) {
	return f.identity, nil
}

func (f fixedIdentityAuth) AuthenticateAccount(_ context.Context, _ string) (CallerIdentity, error) {
	return f.identity, nil
}

// ── CreateOrganization ────────────────────────────────────────────────────────

// TestCreateOrganization_AccountOnlyCaller verifies that a caller with AccountID
// set but no MemberID or ProjectID (account-only, pre-org) can create an
// organization and receives both the org id and global project id.
func TestCreateOrganization_AccountOnlyCaller(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	orgID := uuid.New()
	globalProjectID := uuid.New()

	svc := zeroServer()
	svc.createOrganization = &fakeCreateOrganization{
		result: command.CreateOrganizationResult{
			OrgID:           orgID,
			GlobalProjectID: globalProjectID,
		},
	}

	// Account-only identity: AccountID is set; MemberID and ProjectID are nil.
	auth := fixedIdentityAuth{identity: CallerIdentity{
		AccountID: accountID,
		MemberID:  uuid.Nil,
		ProjectID: uuid.Nil,
		Role:      model.RoleUnspecified,
	}}

	srv, client := startTestServer(svc, connect.WithInterceptors(NewAuthInterceptor(auth, nil)))
	defer srv.Close()

	req := connect.NewRequest(&orakov1.CreateOrganizationRequest{Name: "Acme"})
	req.Header().Set("Authorization", "Bearer dummy") // value ignored by fixedIdentityAuth

	resp, err := client.CreateOrganization(t.Context(), req)
	if err != nil {
		t.Fatalf("CreateOrganization returned error: %v", err)
	}

	if resp.Msg.GetOrganizationId() != orgID.String() {
		t.Errorf("organization_id = %q, want %q", resp.Msg.GetOrganizationId(), orgID.String())
	}

	if resp.Msg.GetGlobalProjectId() != globalProjectID.String() {
		t.Errorf("global_project_id = %q, want %q", resp.Msg.GetGlobalProjectId(), globalProjectID.String())
	}
}

// TestCreateOrganization_MissingAccountID verifies that a caller with no
// AccountID (uuid.Nil, e.g. the dev stub) gets CodeUnauthenticated.
func TestCreateOrganization_MissingAccountID(t *testing.T) {
	t.Parallel()

	svc := zeroServer()

	// DevAuthenticator sets MemberID/ProjectID/Role but leaves AccountID as
	// uuid.Nil, simulating a caller without a resolved account identity.
	srv, client := startTestServerWithAuth(svc)
	defer srv.Close()

	req := connect.NewRequest(&orakov1.CreateOrganizationRequest{Name: "Acme"})
	setAuth(req, "admin") // valid dev token but no AccountID

	_, err := client.CreateOrganization(t.Context(), req)
	assertConnectCode(t, err, connect.CodeUnauthenticated)
}

// TestCreateOrganization_EmptyName verifies that an empty name returns
// CodeInvalidArgument (checked after the AccountID gate passes).
func TestCreateOrganization_EmptyName(t *testing.T) {
	t.Parallel()

	svc := zeroServer()

	auth := fixedIdentityAuth{identity: CallerIdentity{
		AccountID: uuid.New(),
		MemberID:  uuid.Nil,
		ProjectID: uuid.Nil,
		Role:      model.RoleUnspecified,
	}}

	srv, client := startTestServer(svc, connect.WithInterceptors(NewAuthInterceptor(auth, nil)))
	defer srv.Close()

	req := connect.NewRequest(&orakov1.CreateOrganizationRequest{Name: ""})
	req.Header().Set("Authorization", "Bearer dummy")

	_, err := client.CreateOrganization(t.Context(), req)
	assertConnectCode(t, err, connect.CodeInvalidArgument)
}

// TestCreateOrganization_DownstreamErrorMapping verifies that domain errors
// returned by the command handler are translated to the correct Connect codes.
func TestCreateOrganization_DownstreamErrorMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		cmdErr error
		want   connect.Code
	}{
		{
			name:   "InvalidError → CodeInvalidArgument",
			cmdErr: errs.InvalidError{Field: "name", Reason: "too long"},
			want:   connect.CodeInvalidArgument,
		},
		{
			name:   "DuplicateError → CodeAlreadyExists",
			cmdErr: errs.DuplicateError{Resource: "organization"},
			want:   connect.CodeAlreadyExists,
		},
		{
			name:   "InternalError → CodeInternal",
			cmdErr: errs.InternalError{Err: errors.New("db failure")},
			want:   connect.CodeInternal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := zeroServer()
			svc.createOrganization = &fakeCreateOrganization{err: tc.cmdErr}

			auth := fixedIdentityAuth{identity: CallerIdentity{
				AccountID: uuid.New(),
				MemberID:  uuid.Nil,
				ProjectID: uuid.Nil,
				Role:      model.RoleUnspecified,
			}}

			srv, client := startTestServer(svc, connect.WithInterceptors(NewAuthInterceptor(auth, nil)))
			defer srv.Close()

			req := connect.NewRequest(&orakov1.CreateOrganizationRequest{Name: "Acme"})
			req.Header().Set("Authorization", "Bearer dummy")

			_, err := client.CreateOrganization(t.Context(), req)
			assertConnectCode(t, err, tc.want)
		})
	}
}
