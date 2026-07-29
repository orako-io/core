// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/identity"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

func TestAccountStoreResetPasswordConcurrentReplay(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := identity.NewAccountStore(pool)
	email := uuid.NewString() + "@example.test"

	account, err := model.NewAccount(uuid.New(), email, "", "Reset User")
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}

	if err := store.Create(t.Context(), account); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store.SetPassword(t.Context(), account.ID, "initial-hash"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	_, version, ok, err := store.ResetVersionByEmail(t.Context(), email)
	if err != nil || !ok {
		t.Fatalf("ResetVersionByEmail: ok=%v err=%v", ok, err)
	}

	start := make(chan struct{})
	results := make(chan bool, 2)
	errs := make(chan error, 2)

	var workers sync.WaitGroup
	workers.Add(2)

	for _, hash := range []string{"first-new-hash", "second-new-hash"} {
		go func() {
			defer workers.Done()
			<-start

			updated, resetErr := store.ResetPassword(t.Context(), email, version, hash)
			results <- updated
			errs <- resetErr
		}()
	}

	close(start)
	workers.Wait()
	close(results)
	close(errs)

	for resetErr := range errs {
		if resetErr != nil {
			t.Fatalf("ResetPassword: %v", resetErr)
		}
	}

	var updatedCount int
	for updated := range results {
		if updated {
			updatedCount++
		}
	}

	if updatedCount != 1 {
		t.Fatalf("successful conditional updates = %d, want 1", updatedCount)
	}

	_, currentVersion, ok, err := store.ResetVersionByEmail(t.Context(), email)
	if err != nil || !ok {
		t.Fatalf("ResetVersionByEmail after reset: ok=%v err=%v", ok, err)
	}

	if currentVersion != version+1 {
		t.Fatalf("reset version = %d, want %d", currentVersion, version+1)
	}
}
