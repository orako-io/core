// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WithTx runs fn inside a single database transaction.
//
// It begins a transaction, invokes fn, and commits if fn returns nil. If fn
// returns an error — or commit fails — the transaction is rolled back and the
// error is returned. A guard ensures the deferred rollback is a no-op once the
// commit has succeeded, so a committed transaction is never spuriously rolled
// back. fn must use the supplied tx for every statement in the unit of work.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	if state, ok := ctx.Value(txCtxKey{}).(*txState); ok {
		return fn(state.tx)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}

	committed := false

	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	committed = true

	return nil
}
