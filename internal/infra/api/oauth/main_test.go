// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/goleak"
)

// TestMain asserts the package leaves no stray goroutines after each test
// (httptest servers, pgx pools) — mirrors internal/adapters/eventlog's
// convention for Postgres-backed integration tests.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// seedMember inserts a member row so authorization codes and tokens can
// satisfy their member_id foreign key. Returns the new member ID.
func seedMember(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()

	id := uuid.New()

	_, err := pool.Exec(t.Context(),
		"INSERT INTO members (id, display_name, status) VALUES ($1, $2, $3)",
		id, "Test Member", "active")
	if err != nil {
		t.Fatalf("seedMember: %v", err)
	}

	return id
}
