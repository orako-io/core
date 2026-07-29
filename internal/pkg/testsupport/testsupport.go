// SPDX-License-Identifier: Apache-2.0

// Package testsupport wires integration tests to the dev-stack backing
// services (Postgres).
//
// Connection details come from the same environment the server reads, so the
// tests run for real inside `make up` and against any locally reachable stack.
// When the stack is absent the helpers skip by default; set ORAKO_INTEGRATION=1
// to turn an unreachable dependency into a hard failure (used in CI and the dev
// boot gate where the services are guaranteed present).
//
// Runs against a stock postgres:16 image: the migration chain needs only
// pg_trgm (a contrib module bundled with the official Postgres image), no
// pgvector or other non-stock extension.
package testsupport

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/orako-io/core/internal/pkg/config"
	"github.com/orako-io/core/internal/pkg/postgres"
	orakosql "github.com/orako-io/core/sql"
)

// integrationEnv is the flag that promotes an unreachable dependency from a
// skipped test to a failed one.
const integrationEnv = "ORAKO_INTEGRATION"

// RequirePostgres returns a pooled connection to the configured Postgres with
// all migrations applied, or skips the test when Postgres is unreachable.
//
// The pool is closed automatically when the test finishes.
func RequirePostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()

	conf := loadConfig(t)

	pool, err := postgres.Connect(t.Context(), conf.PostgresDSN)
	if err != nil {
		skipOrFail(t, "postgres", err)
	}

	if err := orakosql.RunMigrations(conf.PostgresDSN); err != nil {
		pool.Close()
		t.Fatalf("running migrations: %v", err)
	}

	t.Cleanup(pool.Close)

	return pool
}

// loadConfig resolves the runtime configuration from the process environment.
func loadConfig(t *testing.T) config.Config {
	t.Helper()

	conf, err := config.Load(os.Getenv)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	return conf
}

// skipOrFail skips the test when integration mode is off, and fails it when on.
func skipOrFail(t *testing.T, dependency string, err error) {
	t.Helper()

	if os.Getenv(integrationEnv) == "1" {
		t.Fatalf("%s is required (%s=1) but unreachable: %v", dependency, integrationEnv, err)
	}

	t.Skipf("skipping: %s unreachable (set %s=1 to require it): %v", dependency, integrationEnv, err)
}
