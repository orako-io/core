// SPDX-License-Identifier: AGPL-3.0-or-later

package testsupport

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/orako-io/core/internal/adapters/conversation"
	"github.com/orako-io/core/internal/application/domain/model"
)

// The seed helpers below insert the foreign-key parents an integration test needs
// before exercising a store. They are shared by every bounded-context adapter's
// tests, so they live here rather than being duplicated per package. Most are raw
// SQL; a couple compose the conversation store because a conversation is not a
// single-row insert.

// SeedProject inserts a project and returns its ID.
func SeedProject(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()

	id := uuid.New()
	if _, err := pool.Exec(t.Context(), "INSERT INTO projects (id, name) VALUES ($1, $2)", id, "test-project"); err != nil {
		t.Fatalf("SeedProject: %v", err)
	}

	return id
}

// SeedMember inserts an active member and returns its ID.
func SeedMember(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()

	id := uuid.New()
	if _, err := pool.Exec(t.Context(),
		"INSERT INTO members (id, display_name, status) VALUES ($1, $2, $3)", id, "Test User", "active"); err != nil {
		t.Fatalf("SeedMember: %v", err)
	}

	return id
}

// SeedOrganization inserts an organization and returns its ID.
func SeedOrganization(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()

	id := uuid.New()
	if _, err := pool.Exec(t.Context(), "INSERT INTO organizations (id, name) VALUES ($1, $2)", id, "Test Org"); err != nil {
		t.Fatalf("SeedOrganization: %v", err)
	}

	return id
}

// SeedProjectInOrg inserts a project owned by orgID and returns its ID.
func SeedProjectInOrg(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) uuid.UUID {
	t.Helper()

	id := uuid.New()
	if _, err := pool.Exec(t.Context(),
		"INSERT INTO projects (id, name, org_id) VALUES ($1, $2, $3)", id, "test-project", orgID); err != nil {
		t.Fatalf("SeedProjectInOrg: %v", err)
	}

	return id
}

// SeedAccountForOrg inserts an account (the org creator) and returns its ID.
func SeedAccountForOrg(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()

	id := uuid.New()
	if _, err := pool.Exec(t.Context(),
		"INSERT INTO accounts (id, email) VALUES ($1, $2)", id, uuid.NewString()+"@example.test"); err != nil {
		t.Fatalf("SeedAccountForOrg: %v", err)
	}

	return id
}

// SeedConversation seeds a project, an asker, and an open conversation, returning
// the conversation ID.
func SeedConversation(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()

	projectID := SeedProject(t, pool)
	askerID := SeedMember(t, pool)

	conv, err := model.NewConversation(uuid.New(), projectID, askerID, "What is X?")
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}

	if err := conversation.NewStore(pool).CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	return conv.ID
}

// SeedAlertCandidate seeds a backdated conversation in projectID with one active
// and one excluded pool candidate, returning the conversation ID. age backdates
// created_at so escalation-timeout tests can trip the threshold.
func SeedAlertCandidate(t *testing.T, pool *pgxpool.Pool, projectID uuid.UUID, age time.Duration) uuid.UUID {
	t.Helper()

	askerID := SeedMember(t, pool)

	conv, err := model.NewConversation(uuid.New(), projectID, askerID, "Which index fits?")
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}

	if err := conversation.NewStore(pool).CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	if _, err := pool.Exec(t.Context(),
		"UPDATE conversations SET created_at = $2 WHERE id = $1", conv.ID, time.Now().UTC().Add(-age)); err != nil {
		t.Fatalf("backdating created_at: %v", err)
	}

	activeID := SeedMember(t, pool)
	excludedID := SeedMember(t, pool)

	if _, err := pool.Exec(t.Context(),
		"INSERT INTO conversation_members (conversation_id, member_id, invited_at) VALUES ($1, $2, NOW())", conv.ID, activeID); err != nil {
		t.Fatalf("seeding active candidate: %v", err)
	}

	if _, err := pool.Exec(t.Context(),
		"INSERT INTO conversation_members (conversation_id, member_id, invited_at, excluded_at) VALUES ($1, $2, NOW(), NOW())", conv.ID, excludedID); err != nil {
		t.Fatalf("seeding excluded candidate: %v", err)
	}

	return conv.ID
}
