// SPDX-License-Identifier: AGPL-3.0-or-later

package conversation_test

import (
	"testing"
	"time"

	"github.com/orako-io/core/internal/adapters/conversation"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestEscalationStore_ExpiryTransition proves the new expiry rung against a real
// database: a pool conversation older than the expiry threshold (alert timeout ×
// ExpiryAlertMultiplier) is listed by UnclaimedForExpiry and transitions to
// timed_out via MarkExpired exactly once; one only past the alert timeout (but
// not the far-longer expiry timeout) is left alone.
func TestEscalationStore_ExpiryTransition(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := conversation.NewEscalationStore(pool)
	convStore := conversation.NewStore(pool)
	projectID := testsupport.SeedProject(t, pool)

	defaultAlertSeconds := int64(model.DefaultAlertTimeout / time.Second)

	// Older than the 24h expiry threshold: eligible.
	expiredID := testsupport.SeedAlertCandidate(t, pool, projectID, model.DefaultExpiryTimeout+time.Hour)
	// Past the 4h alert timeout but well under expiry: must NOT be listed.
	freshID := testsupport.SeedAlertCandidate(t, pool, projectID, model.DefaultAlertTimeout+time.Hour)

	rows, err := store.UnclaimedForExpiry(t.Context(), defaultAlertSeconds)
	if err != nil {
		t.Fatalf("UnclaimedForExpiry: %v", err)
	}

	// The DB is shared across parallel tests, so assert membership, not counts.
	inList := map[string]bool{}
	for _, r := range rows {
		inList[r.ID.String()] = true
	}

	if !inList[expiredID.String()] {
		t.Errorf("expired conversation %s not listed for expiry", expiredID)
	}

	if inList[freshID.String()] {
		t.Errorf("conversation %s past alert-but-not-expiry must not be listed", freshID)
	}

	won, err := store.MarkExpired(t.Context(), expiredID)
	if err != nil {
		t.Fatalf("MarkExpired: %v", err)
	}

	if !won {
		t.Fatal("MarkExpired must win the first transition")
	}

	conv, err := convStore.ConversationByID(t.Context(), expiredID)
	if err != nil {
		t.Fatalf("ConversationByID: %v", err)
	}

	if conv.Status != model.ConversationStatusTimedOut {
		t.Errorf("status = %q, want timed_out", conv.Status)
	}

	// Idempotent: a second CAS finds status already off 'open' and loses.
	wonAgain, err := store.MarkExpired(t.Context(), expiredID)
	if err != nil {
		t.Fatalf("MarkExpired (second): %v", err)
	}

	if wonAgain {
		t.Error("MarkExpired must not win twice for the same conversation")
	}
}
