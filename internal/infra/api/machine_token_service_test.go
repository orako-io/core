// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/query"
)

func TestCreateMachineToken_CallerFromIdentity(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	memberID := uuid.New()
	projID := uuid.New()
	tokenID := uuid.New()
	now := time.Now()

	svc := zeroServer()
	create := &fakeCreateMachineToken{result: command.CreateMachineTokenResult{
		ID:         tokenID,
		Label:      "CI agent",
		ProjectIDs: []uuid.UUID{projID},
		CreatedAt:  now,
		ExpiresAt:  now.Add(365 * 24 * time.Hour),
		Secret:     "mcp_at_super-secret",
	}}
	svc.createMachineToken = create

	auth := fixedIdentityAuth{identity: adminIdentity(orgID, memberID)}
	srv, client := startTestServer(svc, connect.WithInterceptors(NewAuthInterceptor(auth, nil)))
	defer srv.Close()

	req := connect.NewRequest(&orakov1.CreateMachineTokenRequest{
		Label:      "CI agent",
		ProjectIds: []string{projID.String()},
	})
	req.Header().Set("Authorization", "Bearer dummy")

	resp, err := client.CreateMachineToken(t.Context(), req)
	if err != nil {
		t.Fatalf("CreateMachineToken: %v", err)
	}

	// The secret is returned exactly once, in this response.
	if resp.Msg.GetSecret() != "mcp_at_super-secret" {
		t.Errorf("secret = %q, want mcp_at_super-secret", resp.Msg.GetSecret())
	}

	if resp.Msg.GetToken().GetId() != tokenID.String() || resp.Msg.GetToken().GetLabel() != "CI agent" {
		t.Errorf("token = %+v, want id=%s label=CI agent", resp.Msg.GetToken(), tokenID)
	}

	// The caller identity supplies MemberID/OrgID/IsOrgAdmin, never the request.
	if create.got.MemberID != memberID {
		t.Errorf("command MemberID = %s, want the caller %s", create.got.MemberID, memberID)
	}

	if create.got.OrgID != orgID {
		t.Errorf("command OrgID = %s, want the caller's org %s", create.got.OrgID, orgID)
	}

	if !create.got.IsOrgAdmin {
		t.Error("command IsOrgAdmin = false, want true (from identity)")
	}

	if create.got.Label != "CI agent" {
		t.Errorf("command Label = %q, want CI agent", create.got.Label)
	}

	if len(create.got.ProjectIDs) != 1 || create.got.ProjectIDs[0] != projID {
		t.Errorf("command ProjectIDs = %v, want [%s]", create.got.ProjectIDs, projID)
	}
}

func TestCreateMachineToken_RejectsNonAdmin(t *testing.T) {
	t.Parallel()

	svc := zeroServer()

	auth := fixedIdentityAuth{identity: nonAdminIdentity()}
	srv, client := startTestServer(svc, connect.WithInterceptors(NewAuthInterceptor(auth, nil)))
	defer srv.Close()

	req := connect.NewRequest(&orakov1.CreateMachineTokenRequest{Label: "x"})
	req.Header().Set("Authorization", "Bearer dummy")

	_, err := client.CreateMachineToken(t.Context(), req)
	assertConnectCode(t, err, connect.CodePermissionDenied)
}

func TestListMachineTokens_CallerFromIdentity(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	memberID := uuid.New()
	now := time.Now()

	svc := zeroServer()
	list := &fakeListMachineTokens{views: []query.MachineTokenView{
		{ID: uuid.New(), Label: "CI agent", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
	}}
	svc.listMachineTokens = list

	auth := fixedIdentityAuth{identity: adminIdentity(orgID, memberID)}
	srv, client := startTestServer(svc, connect.WithInterceptors(NewAuthInterceptor(auth, nil)))
	defer srv.Close()

	req := connect.NewRequest(&orakov1.ListMachineTokensRequest{})
	req.Header().Set("Authorization", "Bearer dummy")

	resp, err := client.ListMachineTokens(t.Context(), req)
	if err != nil {
		t.Fatalf("ListMachineTokens: %v", err)
	}

	if len(resp.Msg.GetTokens()) != 1 || resp.Msg.GetTokens()[0].GetLabel() != "CI agent" {
		t.Errorf("tokens = %+v, want one entry labeled CI agent", resp.Msg.GetTokens())
	}

	if list.got.OrgID != orgID {
		t.Errorf("query OrgID = %s, want the caller's org %s", list.got.OrgID, orgID)
	}
}

func TestListMachineTokens_RejectsNonAdmin(t *testing.T) {
	t.Parallel()

	svc := zeroServer()

	auth := fixedIdentityAuth{identity: nonAdminIdentity()}
	srv, client := startTestServer(svc, connect.WithInterceptors(NewAuthInterceptor(auth, nil)))
	defer srv.Close()

	req := connect.NewRequest(&orakov1.ListMachineTokensRequest{})
	req.Header().Set("Authorization", "Bearer dummy")

	_, err := client.ListMachineTokens(t.Context(), req)
	assertConnectCode(t, err, connect.CodePermissionDenied)
}

func TestRevokeMachineToken_CallerFromIdentity(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	tokenID := uuid.New()

	svc := zeroServer()
	revoke := &fakeRevokeMachineToken{}
	svc.revokeMachineToken = revoke

	auth := fixedIdentityAuth{identity: adminIdentity(orgID, uuid.New())}
	srv, client := startTestServer(svc, connect.WithInterceptors(NewAuthInterceptor(auth, nil)))
	defer srv.Close()

	req := connect.NewRequest(&orakov1.RevokeMachineTokenRequest{Id: tokenID.String()})
	req.Header().Set("Authorization", "Bearer dummy")

	if _, err := client.RevokeMachineToken(t.Context(), req); err != nil {
		t.Fatalf("RevokeMachineToken: %v", err)
	}

	// The caller's OrgID (not MemberID) supplies the revoke's scope: any
	// admin of the org may revoke a token they did not personally mint.
	if revoke.got.OrgID != orgID {
		t.Errorf("command OrgID = %s, want the caller's org %s", revoke.got.OrgID, orgID)
	}

	if revoke.got.TokenID != tokenID {
		t.Errorf("command TokenID = %s, want %s", revoke.got.TokenID, tokenID)
	}
}

func TestRevokeMachineToken_RejectsNonAdmin(t *testing.T) {
	t.Parallel()

	svc := zeroServer()

	auth := fixedIdentityAuth{identity: nonAdminIdentity()}
	srv, client := startTestServer(svc, connect.WithInterceptors(NewAuthInterceptor(auth, nil)))
	defer srv.Close()

	req := connect.NewRequest(&orakov1.RevokeMachineTokenRequest{Id: uuid.NewString()})
	req.Header().Set("Authorization", "Bearer dummy")

	_, err := client.RevokeMachineToken(t.Context(), req)
	assertConnectCode(t, err, connect.CodePermissionDenied)
}
