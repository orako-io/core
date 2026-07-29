// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/query"
)

// adminIdentity is an org-admin caller: the join-code RPCs are admin-gated by
// the interceptor, so a non-admin identity is rejected before the handler runs.
func adminIdentity(orgID, memberID uuid.UUID) CallerIdentity {
	return CallerIdentity{
		AccountID:  uuid.New(),
		MemberID:   memberID,
		ProjectID:  uuid.New(),
		ProjectIDs: []uuid.UUID{uuid.New()},
		Role:       model.RoleAdmin,
		OrgID:      orgID,
		IsOrgAdmin: true,
	}
}

// nonAdminIdentity is a plain (non-org-admin) caller: used by every RPC
// test that only needs to prove the interceptor's admin gate rejects it,
// without caring about the rest of the identity.
func nonAdminIdentity() CallerIdentity {
	return CallerIdentity{
		AccountID:  uuid.New(),
		MemberID:   uuid.New(),
		ProjectID:  uuid.New(),
		ProjectIDs: []uuid.UUID{uuid.New()},
		Role:       model.RoleSpecialist,
		OrgID:      uuid.New(),
		IsOrgAdmin: false,
	}
}

func TestGenerateJoinCode_OrgFromIdentity(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	memberID := uuid.New()
	projID := uuid.New()

	svc := zeroServer()
	gen := &fakeGenerateJoinCode{result: command.GenerateJoinCodeResult{
		Code:        "shiny-code",
		ProjectID:   projID,
		ProjectName: "default",
	}}
	svc.generateJoinCode = gen

	auth := fixedIdentityAuth{identity: adminIdentity(orgID, memberID)}
	srv, client := startTestServer(svc, connect.WithInterceptors(NewAuthInterceptor(auth, nil)))
	defer srv.Close()

	// project_id empty → the org's default project.
	req := connect.NewRequest(&orakov1.GenerateJoinCodeRequest{})
	req.Header().Set("Authorization", "Bearer dummy")

	resp, err := client.GenerateJoinCode(t.Context(), req)
	if err != nil {
		t.Fatalf("GenerateJoinCode: %v", err)
	}

	if resp.Msg.GetCode() != "shiny-code" || resp.Msg.GetProjectId() != projID.String() || resp.Msg.GetProjectName() != "default" {
		t.Errorf("response = (%q,%q,%q), want (shiny-code,%s,default)",
			resp.Msg.GetCode(), resp.Msg.GetProjectId(), resp.Msg.GetProjectName(), projID)
	}

	// The org and actor must come from the caller identity, never the request body.
	if gen.got.OrgID != orgID {
		t.Errorf("command OrgID = %s, want the caller's org %s (from identity)", gen.got.OrgID, orgID)
	}

	if gen.got.ActorMemberID != memberID {
		t.Errorf("command ActorMemberID = %s, want the caller %s", gen.got.ActorMemberID, memberID)
	}

	if gen.got.ProjectID != uuid.Nil {
		t.Errorf("command ProjectID = %s, want nil for an empty project_id", gen.got.ProjectID)
	}
}

func TestGenerateJoinCode_ExplicitProjectPassedThrough(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	projID := uuid.New()

	svc := zeroServer()
	gen := &fakeGenerateJoinCode{result: command.GenerateJoinCodeResult{Code: "c", ProjectID: projID, ProjectName: "x"}}
	svc.generateJoinCode = gen

	auth := fixedIdentityAuth{identity: adminIdentity(orgID, uuid.New())}
	srv, client := startTestServer(svc, connect.WithInterceptors(NewAuthInterceptor(auth, nil)))
	defer srv.Close()

	req := connect.NewRequest(&orakov1.GenerateJoinCodeRequest{ProjectId: projID.String()})
	req.Header().Set("Authorization", "Bearer dummy")

	if _, err := client.GenerateJoinCode(t.Context(), req); err != nil {
		t.Fatalf("GenerateJoinCode: %v", err)
	}

	if gen.got.ProjectID != projID {
		t.Errorf("command ProjectID = %s, want the explicit %s", gen.got.ProjectID, projID)
	}
}

func TestGenerateJoinCode_RejectsNonAdmin(t *testing.T) {
	t.Parallel()

	svc := zeroServer()

	// A non-admin caller must be rejected by the interceptor's admin gate.
	auth := fixedIdentityAuth{identity: CallerIdentity{
		AccountID:  uuid.New(),
		MemberID:   uuid.New(),
		ProjectID:  uuid.New(),
		ProjectIDs: []uuid.UUID{uuid.New()},
		Role:       model.RoleSpecialist,
		OrgID:      uuid.New(),
		IsOrgAdmin: false,
	}}
	srv, client := startTestServer(svc, connect.WithInterceptors(NewAuthInterceptor(auth, nil)))
	defer srv.Close()

	req := connect.NewRequest(&orakov1.GenerateJoinCodeRequest{})
	req.Header().Set("Authorization", "Bearer dummy")

	_, err := client.GenerateJoinCode(t.Context(), req)
	assertConnectCode(t, err, connect.CodePermissionDenied)
}

func TestGetJoinCode_InactiveWhenAbsent(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()

	svc := zeroServer()
	get := &fakeGetJoinCode{view: query.JoinCodeView{Active: false}}
	svc.getJoinCode = get

	auth := fixedIdentityAuth{identity: adminIdentity(orgID, uuid.New())}
	srv, client := startTestServer(svc, connect.WithInterceptors(NewAuthInterceptor(auth, nil)))
	defer srv.Close()

	req := connect.NewRequest(&orakov1.GetJoinCodeRequest{})
	req.Header().Set("Authorization", "Bearer dummy")

	resp, err := client.GetJoinCode(t.Context(), req)
	if err != nil {
		t.Fatalf("GetJoinCode: %v", err)
	}

	if resp.Msg.GetActive() {
		t.Error("active = true, want false when no code exists")
	}

	if resp.Msg.GetCode() != "" || resp.Msg.GetProjectId() != "" {
		t.Errorf("expected empty code/project, got code=%q project=%q", resp.Msg.GetCode(), resp.Msg.GetProjectId())
	}

	if get.got.OrgID != orgID {
		t.Errorf("query OrgID = %s, want the caller's org %s (from identity)", get.got.OrgID, orgID)
	}
}

func TestGetJoinCode_ActiveMapped(t *testing.T) {
	t.Parallel()

	projID := uuid.New()

	svc := zeroServer()
	svc.getJoinCode = &fakeGetJoinCode{view: query.JoinCodeView{
		Code:        "live",
		ProjectID:   projID,
		ProjectName: "default",
		Active:      true,
	}}

	auth := fixedIdentityAuth{identity: adminIdentity(uuid.New(), uuid.New())}
	srv, client := startTestServer(svc, connect.WithInterceptors(NewAuthInterceptor(auth, nil)))
	defer srv.Close()

	req := connect.NewRequest(&orakov1.GetJoinCodeRequest{})
	req.Header().Set("Authorization", "Bearer dummy")

	resp, err := client.GetJoinCode(t.Context(), req)
	if err != nil {
		t.Fatalf("GetJoinCode: %v", err)
	}

	if !resp.Msg.GetActive() || resp.Msg.GetCode() != "live" || resp.Msg.GetProjectId() != projID.String() || resp.Msg.GetProjectName() != "default" {
		t.Errorf("response = (active=%v,%q,%q,%q), want (true,live,%s,default)",
			resp.Msg.GetActive(), resp.Msg.GetCode(), resp.Msg.GetProjectId(), resp.Msg.GetProjectName(), projID)
	}
}

func TestRevokeJoinCode_OrgFromIdentity(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()

	svc := zeroServer()
	rev := &fakeRevokeJoinCode{}
	svc.revokeJoinCode = rev

	auth := fixedIdentityAuth{identity: adminIdentity(orgID, uuid.New())}
	srv, client := startTestServer(svc, connect.WithInterceptors(NewAuthInterceptor(auth, nil)))
	defer srv.Close()

	req := connect.NewRequest(&orakov1.RevokeJoinCodeRequest{})
	req.Header().Set("Authorization", "Bearer dummy")

	if _, err := client.RevokeJoinCode(t.Context(), req); err != nil {
		t.Fatalf("RevokeJoinCode: %v", err)
	}

	if rev.got.OrgID != orgID {
		t.Errorf("command OrgID = %s, want the caller's org %s (from identity)", rev.got.OrgID, orgID)
	}
}
