// SPDX-License-Identifier: AGPL-3.0-or-later

package eventlog

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestStoreAppendThenReplay proves the core persistence contract: appending N
// events to a project assigns a 1-based monotonic sequence and Replay returns
// them in seq order with payloads intact.
func TestStoreAppendThenReplay(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	projectID := testsupport.SeedProject(t, pool)
	store := NewStore(pool)

	const n = 5

	appended := make([]model.Event, n)

	for i := range n {
		ev := newTestEvent(t, projectID, i)
		stored, err := store.Append(t.Context(), ev)
		if err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}

		if stored.Seq != int64(i+1) {
			t.Fatalf("Append[%d]: got Seq=%d, want %d", i, stored.Seq, i+1)
		}

		appended[i] = stored
	}

	// Replay all.
	replayed, err := store.Replay(t.Context(), projectID, 0)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if len(replayed) != n {
		t.Fatalf("Replay: got %d events, want %d", len(replayed), n)
	}

	for i, got := range replayed {
		want := appended[i]

		if got.ID != want.ID {
			t.Errorf("event[%d] ID mismatch: got %v, want %v", i, got.ID, want.ID)
		}

		if got.Seq != want.Seq {
			t.Errorf("event[%d] Seq mismatch: got %v, want %v", i, got.Seq, want.Seq)
		}

		if got.Type != want.Type {
			t.Errorf("event[%d] Type mismatch: got %v, want %v", i, got.Type, want.Type)
		}

		if string(got.Payload) != string(want.Payload) {
			t.Errorf("event[%d] Payload mismatch", i)
		}
	}
}

// TestStoreReplaySinceSeq proves that Replay(sinceSeq=k) returns only events
// with Seq > k, enabling incremental catchup.
func TestStoreReplaySinceSeq(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	projectID := testsupport.SeedProject(t, pool)
	store := NewStore(pool)

	const n = 4

	for i := range n {
		if _, err := store.Append(t.Context(), newTestEvent(t, projectID, i)); err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}

	// Replay since seq 2: should return events at seq 3 and 4.
	tail, err := store.Replay(t.Context(), projectID, 2)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if len(tail) != 2 {
		t.Fatalf("Replay(sinceSeq=2): got %d events, want 2", len(tail))
	}

	if tail[0].Seq != 3 || tail[1].Seq != 4 {
		t.Fatalf("Replay(sinceSeq=2): seq = %v/%v, want 3/4", tail[0].Seq, tail[1].Seq)
	}
}

// TestStoreAppendIsolated proves that two projects share no sequence space: seq
// always starts at 1 for a fresh project.
func TestStoreAppendIsolated(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := NewStore(pool)

	p1 := testsupport.SeedProject(t, pool)
	p2 := testsupport.SeedProject(t, pool)

	for i, pid := range []uuid.UUID{p1, p2} {
		ev, err := store.Append(t.Context(), newTestEvent(t, pid, i))
		if err != nil {
			t.Fatalf("Append(project%d): %v", i, err)
		}

		if ev.Seq != 1 {
			t.Fatalf("Append(project%d): got Seq=%d, want 1", i, ev.Seq)
		}
	}
}

// TestStoreConcurrentAppend proves that concurrent appends under the same
// project produce gapless monotonic sequences with no duplicates. A (project_id,
// seq) unique constraint maps collisions to ErrDuplicate, so callers can retry;
// here we accept retries and verify the final log has exactly N unique entries.
func TestStoreConcurrentAppend(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	projectID := testsupport.SeedProject(t, pool)
	store := NewStore(pool)

	const goroutines = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := range goroutines {
		go func(g int) {
			defer wg.Done()

			ev := newTestEvent(t, projectID, g)

			for {
				_, err := store.Append(t.Context(), ev)
				if err == nil {
					return
				}

				if errors.Is(err, adaptererr.ErrDuplicate) {
					// seq collision: generate a new ID and retry.
					ev.ID = uuid.New()
					continue
				}

				t.Errorf("goroutine %d: unexpected Append error: %v", g, err)

				return
			}
		}(g)
	}

	wg.Wait()

	replayed, err := store.Replay(t.Context(), projectID, 0)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if len(replayed) != goroutines {
		t.Fatalf("concurrent Append: got %d events, want %d", len(replayed), goroutines)
	}

	// Verify gapless monotonic seq.
	for i, ev := range replayed {
		if ev.Seq != int64(i+1) {
			t.Fatalf("event[%d] Seq = %d, want %d (not monotonic)", i, ev.Seq, i+1)
		}
	}
}

// TestStoreAppendPublishOrder proves that Append returns only after the row is
// committed (the goroutine that reads the log AFTER Append must always see the
// event — no read-after-publish race).
func TestStoreAppendPublishOrder(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	projectID := testsupport.SeedProject(t, pool)
	store := NewStore(pool)

	stored, err := store.Append(t.Context(), newTestEvent(t, projectID, 0))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Simulate a publish consumer reading the log immediately after.
	replayed, err := store.Replay(t.Context(), projectID, 0)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if len(replayed) == 0 {
		t.Fatal("event not visible in log immediately after Append (publish-before-commit)")
	}

	if replayed[0].ID != stored.ID {
		t.Fatalf("Replay returned wrong event: got %v, want %v", replayed[0].ID, stored.ID)
	}
}

// TestStoreEventTypeRoundTrip verifies that every EventType survives the
// string round-trip through the DB without loss.
func TestStoreEventTypeRoundTrip(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	projectID := testsupport.SeedProject(t, pool)
	store := NewStore(pool)

	types := []model.EventType{
		model.EventTypeConversationOpened,
		model.EventTypeMessagePosted,
		model.EventTypeConversationClosed,
		model.EventTypeKBEntryCreated,
		model.EventTypeKBAnswerSuperseded,
		model.EventTypeKBFeedback,
		model.EventTypeMemberLifecycle,
		model.EventTypePresenceChanged,
	}

	for _, et := range types {
		t.Run(et.String(), func(t *testing.T) {
			t.Parallel()

			ev, err := model.NewEvent(
				uuid.New(),
				projectID,
				et,
				[]byte(`{"ok":true}`),
				time.Now().UTC(),
			)
			if err != nil {
				t.Fatalf("NewEvent: %v", err)
			}

			stored, err := store.Append(t.Context(), ev)
			if err != nil {
				t.Fatalf("Append: %v", err)
			}

			replayed, err := store.Replay(t.Context(), projectID, stored.Seq-1)
			if err != nil {
				t.Fatalf("Replay: %v", err)
			}

			if len(replayed) == 0 {
				t.Fatal("event not found after append")
			}

			if replayed[0].Type != et {
				t.Fatalf("Type mismatch: got %v, want %v", replayed[0].Type, et)
			}
		})
	}
}
