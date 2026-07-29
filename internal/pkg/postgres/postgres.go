// SPDX-License-Identifier: Apache-2.0

// Package postgres opens and verifies the Orako Postgres connection pool.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Option customises the pool configuration before it is opened.
type Option func(*pgxpool.Config)

// WithTransactionPoolerCompat makes pgx safe to use behind a transaction-mode
// connection pooler (Supabase's Supavisor / PgBouncer), where a connection is
// returned to the pool after every transaction and cached named prepared
// statements would break ("prepared statement does not exist").
//
// It selects QueryExecModeExec: each query uses an unnamed prepared statement
// via the extended protocol with no server-side cache — the pooler-safe option
// recommended by Supabase, while keeping typed/binary parameter binding (arrays,
// timestamps, jsonb) that the simple protocol would flatten to text.
func WithTransactionPoolerCompat() Option {
	return func(cfg *pgxpool.Config) {
		cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	}
}

// Connect opens a pgx connection pool for dsn and verifies it with a ping.
//
// The caller owns the returned pool and must call Close when done. The pool is
// bound to ctx for the lifetime of the connection attempt only.
func Connect(ctx context.Context, dsn string, opts ...Option) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing postgres dsn: %w", err)
	}

	for _, opt := range opts {
		opt(cfg)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating postgres pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()

		return nil, fmt.Errorf("pinging postgres: %w", err)
	}

	return pool, nil
}
