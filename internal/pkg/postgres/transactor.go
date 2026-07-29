// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX is the query surface sqlc's generated New(DBTX) accepts. Both
// *pgxpool.Pool and pgx.Tx satisfy it, which is exactly what lets one repository
// method run against the pool or against an ambient transaction with no change
// to its body. It matches sqlc's generated DBTX method-for-method, so a value of
// this type is assignable to any context package's New(DBTX).
type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// txCtxKey carries the ambient transaction state on the context. Unexported so
// the only way to open/join a transaction is through Transactor.WithTx.
type txCtxKey struct{}

type txState struct {
	tx          pgx.Tx
	afterCommit []func()
}

// Conn returns the transaction carried by ctx when a Transactor opened one,
// otherwise pool. Repositories call New(Conn(ctx, s.pool)) so the very same
// method joins a caller's transaction or runs standalone — the application
// composes a multi-repository unit of work without any repository knowing it is
// inside a transaction.
func Conn(ctx context.Context, pool *pgxpool.Pool) DBTX {
	if state, ok := ctx.Value(txCtxKey{}).(*txState); ok {
		return state.tx
	}

	return pool
}

// AfterCommit schedules fn to run after the ambient transaction commits. It
// returns false when ctx is not transactional, in which case callers should run
// fn immediately. Callbacks must be short and non-blocking; they are intended
// for wakeups and cache invalidation, not external I/O.
func AfterCommit(ctx context.Context, fn func()) bool {
	state, ok := ctx.Value(txCtxKey{}).(*txState)
	if !ok {
		return false
	}

	state.afterCommit = append(state.afterCommit, fn)

	return true
}

// Transactor runs a unit of work spanning several repositories in one
// transaction, propagated through the context passed to fn. It is the concrete
// implementation of the application's transaction port, so a use case can wrap
// repository calls in a transaction while depending only on that interface —
// never on pgx.
type Transactor struct {
	pool *pgxpool.Pool
}

// NewTransactor builds a Transactor over pool.
func NewTransactor(pool *pgxpool.Pool) *Transactor {
	return &Transactor{pool: pool}
}

// WithTx runs fn inside a transaction whose handle rides on the context handed
// to fn; every repository call made with that context (via Conn) joins it. If
// ctx already carries a transaction — a nested WithTx from a composed use case —
// fn joins the outer transaction instead of opening a second one, so the whole
// composition commits or rolls back atomically. On any error from fn (or a failed
// commit) the transaction is rolled back.
func (t *Transactor) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txCtxKey{}).(*txState); ok {
		return fn(ctx) // already in a transaction: join it, don't nest a second one
	}

	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}

	committed := false

	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	state := &txState{tx: tx, afterCommit: make([]func(), 0)}
	if err := fn(context.WithValue(ctx, txCtxKey{}, state)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	committed = true

	for _, callback := range state.afterCommit {
		callback()
	}

	return nil
}
