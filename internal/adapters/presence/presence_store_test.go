// SPDX-License-Identifier: AGPL-3.0-or-later

package presence_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/presence"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestPresenceStoreUpsertAndGet proves the upsert-then-get round trip: the
// stored online/offline state matches what was written.
func TestPresenceStoreUpsertAndGet(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := presence.NewStore(pool)

	cases := []struct {
		name   string
		online bool
	}{
		{name: "online", online: true},
		{name: "offline", online: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Each sub-test needs its own member to avoid contention.
			pool2 := testsupport.RequirePostgres(t)
			mID := testsupport.SeedMember(t, pool2)
			store2 := presence.NewStore(pool2)

			presence, err := model.NewPresence(mID, tc.online, time.Now().UTC())
			if err != nil {
				t.Fatalf("NewPresence: %v", err)
			}

			if err := store2.Upsert(t.Context(), presence); err != nil {
				t.Fatalf("Upsert: %v", err)
			}

			got, err := store2.ByMember(t.Context(), mID)
			if err != nil {
				t.Fatalf("ByMember: %v", err)
			}

			if got.MemberID != mID {
				t.Errorf("MemberID: got %v, want %v", got.MemberID, mID)
			}

			if got.Online != tc.online {
				t.Errorf("Online: got %v, want %v", got.Online, tc.online)
			}
		})
	}

	_ = store // keep reference valid

	// Not-found path.
	t.Run("not_found", func(t *testing.T) {
		t.Parallel()

		pool3 := testsupport.RequirePostgres(t)
		store3 := presence.NewStore(pool3)

		_, err := store3.ByMember(t.Context(), uuid.New())
		if !errors.Is(err, adaptererr.ErrNotFound) {
			t.Fatalf("ByMember(unknown): got %v, want ErrNotFound", err)
		}
	})
}

// TestPresenceStoreUpsertIdempotent proves that repeated upserts converge to the
// latest state — the second call overwrites the first.
func TestPresenceStoreUpsertIdempotent(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	memberID := testsupport.SeedMember(t, pool)
	store := presence.NewStore(pool)

	online := func(b bool) model.Presence {
		p, err := model.NewPresence(memberID, b, time.Now().UTC())
		if err != nil {
			t.Fatalf("NewPresence: %v", err)
		}

		return p
	}

	// Write online.
	if err := store.Upsert(t.Context(), online(true)); err != nil {
		t.Fatalf("Upsert(online): %v", err)
	}

	// Overwrite with offline.
	if err := store.Upsert(t.Context(), online(false)); err != nil {
		t.Fatalf("Upsert(offline): %v", err)
	}

	got, err := store.ByMember(t.Context(), memberID)
	if err != nil {
		t.Fatalf("ByMember: %v", err)
	}

	if got.Online {
		t.Fatal("expected offline after second upsert")
	}
}
