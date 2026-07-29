// SPDX-License-Identifier: AGPL-3.0-or-later

package licensing_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/licensing"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestLicenseStoreLifecycle proves the full store contract against real Postgres:
// an empty instance (found=false, no error) → Set → Get round-trip → upsert
// replace (still one row) → Clear → empty again (Clear is idempotent).
func TestLicenseStoreLifecycle(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := licensing.NewLicenseStore(pool)

	// ── No key yet: found=false, nil error (fail-safe → Community at boot). ──
	if _, _, _, found, err := store.Get(t.Context()); err != nil || found {
		t.Fatalf("Get (none): found=%v err=%v, want found=false err=nil", found, err)
	}

	// ── Set → Get round-trip. ─────────────────────────────────────────────────
	admin := uuid.New()
	if err := store.Set(t.Context(), "key-one", admin); err != nil {
		t.Fatalf("Set: %v", err)
	}

	key, setBy, setAt, found, err := store.Get(t.Context())
	if err != nil || !found {
		t.Fatalf("Get (after set): found=%v err=%v", found, err)
	}

	if key != "key-one" || setBy != admin {
		t.Errorf("got key=%q setBy=%s, want key-one / %s", key, setBy, admin)
	}

	if setAt.IsZero() {
		t.Error("setAt is zero; want the insert timestamp")
	}

	// ── Upsert replace: a second Set overwrites the single row. ───────────────
	admin2 := uuid.New()
	if err := store.Set(t.Context(), "key-two", admin2); err != nil {
		t.Fatalf("Set (replace): %v", err)
	}

	key, setBy, _, found, err = store.Get(t.Context())
	if err != nil || !found {
		t.Fatalf("Get (after replace): found=%v err=%v", found, err)
	}

	if key != "key-two" || setBy != admin2 {
		t.Errorf("got key=%q setBy=%s, want key-two / %s", key, setBy, admin2)
	}

	// ── A nil setter (automatic refresh-loop renewal) stores NULL set_by. ─────
	if err := store.Set(t.Context(), "key-renewed", uuid.Nil); err != nil {
		t.Fatalf("Set (nil setter): %v", err)
	}

	if _, setBy, _, _, err = store.Get(t.Context()); err != nil || setBy != uuid.Nil {
		t.Errorf("nil-setter round-trip: setBy=%s err=%v, want uuid.Nil", setBy, err)
	}

	// ── Clear → empty again; Clear is idempotent. ─────────────────────────────
	if err := store.Clear(t.Context()); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	if _, _, _, found, err := store.Get(t.Context()); err != nil || found {
		t.Fatalf("Get (after clear): found=%v err=%v, want found=false", found, err)
	}

	if err := store.Clear(t.Context()); err != nil {
		t.Fatalf("Clear (idempotent): %v", err)
	}
}
