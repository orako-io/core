// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// addMemberToProject links memberID into projectID's roster — the join
// Store.ListMachineTokens/RevokeMachineToken walk (via project_members ⋈
// projects) to resolve which org a machine token's owning member belongs to.
func addMemberToProject(t *testing.T, pool *pgxpool.Pool, projectID, memberID uuid.UUID) {
	t.Helper()

	if _, err := pool.Exec(
		t.Context(),
		"INSERT INTO project_members (project_id, member_id) VALUES ($1, $2)",
		projectID, memberID,
	); err != nil {
		t.Fatalf("addMemberToProject: %v", err)
	}
}

// TestMintMachineTokenNoRefresh proves MintMachineToken yields exactly one
// access-kind row — no paired refresh token — bound to the given resource
// (audience), attributed to the reserved MachineClientID, with its label
// persisted.
func TestMintMachineTokenNoRefresh(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := NewStore(pool)
	orgID := testsupport.SeedOrganization(t, pool)
	memberID := seedMember(t, pool)
	resource := "https://orako.example.com/mcp"
	now := time.Now()

	secret, tok, err := MintMachineToken(t.Context(), store, now, orgID, memberID, resource, nil, "CI agent")
	if err != nil {
		t.Fatalf("MintMachineToken: %v", err)
	}

	if !IsAccessTokenSecret(secret) {
		t.Errorf("secret %q does not carry the mcp_at_ prefix", secret)
	}

	if tok.ClientID != MachineClientID {
		t.Errorf("ClientID = %q, want the reserved %q", tok.ClientID, MachineClientID)
	}

	if tok.Resource != resource {
		t.Errorf("Resource = %q, want %q", tok.Resource, resource)
	}

	if tok.Kind != TokenKindAccess {
		t.Errorf("Kind = %q, want access", tok.Kind)
	}

	if tok.CreatedAt.IsZero() {
		t.Error("CreatedAt was never populated from the database default")
	}

	// The minted secret authenticates as an access token...
	got, err := store.GetToken(t.Context(), secret, TokenKindAccess)
	if err != nil {
		t.Fatalf("GetToken(access): %v", err)
	}

	if got.MemberID != memberID || got.Resource != resource {
		t.Errorf("GetToken(access) mismatch: got %+v", got)
	}

	// ...but there is NO refresh token pair minted alongside it — the whole
	// point of a machine token vs. an OAuth-flow token.
	if _, err := store.GetToken(t.Context(), secret, TokenKindRefresh); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("a machine token must have no refresh row: GetToken(refresh) = %v, want ErrNotFound", err)
	}
}

// TestMintMachineTokenProjectScope proves the requested project scope
// round-trips through the mint, order-preserved.
func TestMintMachineTokenProjectScope(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := NewStore(pool)
	orgID := testsupport.SeedOrganization(t, pool)
	memberID := seedMember(t, pool)
	p1, p2 := uuid.New(), uuid.New()

	secret, tok, err := MintMachineToken(t.Context(), store, time.Now(), orgID, memberID, "https://orako.example.com/mcp", []uuid.UUID{p1, p2}, "scoped")
	if err != nil {
		t.Fatalf("MintMachineToken: %v", err)
	}

	if len(tok.ProjectIDs) != 2 || tok.ProjectIDs[0] != p1 || tok.ProjectIDs[1] != p2 {
		t.Errorf("mint ProjectIDs = %v, want [%s %s]", tok.ProjectIDs, p1, p2)
	}

	got, err := store.GetToken(t.Context(), secret, TokenKindAccess)
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}

	if len(got.ProjectIDs) != 2 || got.ProjectIDs[0] != p1 || got.ProjectIDs[1] != p2 {
		t.Errorf("stored ProjectIDs = %v, want [%s %s]", got.ProjectIDs, p1, p2)
	}
}

// TestStoreListAndRevokeMachineTokensPerOrg proves ListMachineTokens and
// RevokeMachineToken are scoped to the ORG, not the minting member — a
// machine token is org infrastructure, so a SECOND admin of the same org must
// see and be able to revoke a token the FIRST admin minted — while an admin
// of a DIFFERENT org must never see or revoke it (no cross-org leakage). Org
// membership is resolved via the owning member's CURRENT project
// memberships (project_members ⋈ projects.org_id), not the token's own
// (possibly empty) project_ids.
func TestStoreListAndRevokeMachineTokensPerOrg(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := NewStore(pool)
	resource := "https://orako.example.com/mcp"

	orgA := testsupport.SeedOrganization(t, pool)
	orgB := testsupport.SeedOrganization(t, pool)
	projA := testsupport.SeedProjectInOrg(t, pool, orgA)
	projB := testsupport.SeedProjectInOrg(t, pool, orgB)

	adminA1 := seedMember(t, pool) // mints the token
	adminA2 := seedMember(t, pool) // a SECOND admin, same org — never minted anything
	adminB := seedMember(t, pool)  // a DIFFERENT org entirely

	addMemberToProject(t, pool, projA, adminA1)
	addMemberToProject(t, pool, projA, adminA2)
	addMemberToProject(t, pool, projB, adminB)

	_, tok, err := MintMachineToken(t.Context(), store, time.Now(), orgA, adminA1, resource, nil, "CI agent")
	if err != nil {
		t.Fatalf("MintMachineToken: %v", err)
	}

	// A live OAuth-flow token for the SAME minting member must never leak
	// into ListMachineTokens (it filters by MachineClientID).
	client := seedClient(t, store)
	seedTokenPair(t, store, orgA, adminA1, client.ID, resource, uuid.New(), nil)

	// The SECOND admin of org A (who minted nothing) sees the token the
	// FIRST admin minted — a machine token is org infrastructure, not a
	// personal session.
	list, err := store.ListMachineTokens(t.Context(), orgA)
	if err != nil {
		t.Fatalf("ListMachineTokens(orgA): %v", err)
	}

	if len(list) != 1 || list[0].ID != tok.ID || list[0].Label != "CI agent" {
		t.Fatalf("ListMachineTokens(orgA) = %+v, want exactly the one machine token", list)
	}

	if list[0].RevokedAt != nil {
		t.Error("a freshly minted token must not be revoked")
	}

	// org B must never see org A's token.
	otherOrgList, err := store.ListMachineTokens(t.Context(), orgB)
	if err != nil {
		t.Fatalf("ListMachineTokens(orgB): %v", err)
	}

	if len(otherOrgList) != 0 {
		t.Errorf("ListMachineTokens(orgB) leaked org A's token: %+v", otherOrgList)
	}

	// org B (adminB) must not be able to revoke org A's token even knowing
	// its id — no cross-org leakage.
	if err := store.RevokeMachineToken(t.Context(), orgB, tok.ID); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("revoking from the wrong org: got %v, want ErrNotFound", err)
	}

	// The SECOND admin of org A (adminA2) CAN revoke the token adminA1
	// minted — incident response / offboarding must not require the
	// original minting admin to still be around.
	if err := store.RevokeMachineToken(t.Context(), orgA, tok.ID); err != nil {
		t.Fatalf("RevokeMachineToken(orgA): %v", err)
	}

	after, err := store.ListMachineTokens(t.Context(), orgA)
	if err != nil {
		t.Fatalf("ListMachineTokens(orgA) after revoke: %v", err)
	}

	if len(after) != 1 || after[0].RevokedAt == nil {
		t.Fatalf("ListMachineTokens(orgA) after revoke = %+v, want the token marked revoked", after)
	}

	// Revoking again is a well-defined not-found, not a silent success.
	if err := store.RevokeMachineToken(t.Context(), orgA, tok.ID); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("re-revoking: got %v, want ErrNotFound", err)
	}
}
