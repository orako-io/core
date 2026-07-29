// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/orako-io/core/internal/pkg/postgres"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

func TestTransactorAfterCommit(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	txor := postgres.NewTransactor(pool)
	called := false

	if err := txor.WithTx(t.Context(), func(ctx context.Context) error {
		if !postgres.AfterCommit(ctx, func() { called = true }) {
			t.Fatal("AfterCommit did not detect ambient transaction")
		}

		return nil
	}); err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	if !called {
		t.Fatal("after-commit callback was not called")
	}
}

func TestTransactorDropsAfterCommitOnRollback(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	txor := postgres.NewTransactor(pool)
	called := false
	wantErr := errors.New("rollback")

	err := txor.WithTx(t.Context(), func(ctx context.Context) error {
		postgres.AfterCommit(ctx, func() { called = true })

		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("WithTx error = %v, want %v", err, wantErr)
	}

	if called {
		t.Fatal("after-commit callback ran after rollback")
	}
}
