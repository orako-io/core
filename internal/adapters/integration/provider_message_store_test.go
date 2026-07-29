// SPDX-License-Identifier: AGPL-3.0-or-later

package integration_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/integration"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestProviderMessageStore_UpsertAndByConversation proves the basic
// insert-then-list round trip.
func TestProviderMessageStore_UpsertAndByConversation(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProviderMessageStore(pool)
	convID := testsupport.SeedConversation(t, pool)
	memberID := testsupport.SeedMember(t, pool)

	msg := model.ProviderMessage{
		ID:             uuid.New(),
		ConversationID: convID,
		MemberID:       memberID,
		ProviderKind:   "slack",
		ChannelID:      "DDMCHANNEL",
		MessageRef:     "1111111111.000001",
		State:          model.ProviderMessageStatePosted,
	}

	if err := store.Upsert(t.Context(), msg); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	rows, err := store.ByConversation(t.Context(), convID)
	if err != nil {
		t.Fatalf("ByConversation: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}

	if rows[0].ChannelID != "DDMCHANNEL" || rows[0].MessageRef != "1111111111.000001" {
		t.Errorf("row = %+v, want channel DDMCHANNEL ref 1111111111.000001", rows[0])
	}

	if rows[0].State != model.ProviderMessageStatePosted {
		t.Errorf("state = %q, want posted", rows[0].State)
	}
}

// TestProviderMessageStore_UpsertConflictIsANoOp proves the
// UNIQUE(conversation_id, member_id) constraint collapses a repeat delivery
// into a single row (no duplicate) AND that the conflict never overwrites the
// first row: ON CONFLICT DO NOTHING, so a replayed CONVERSATION_OPENED can
// never regress an already-advanced ledger row (e.g. claimed_won) back to
// "posted", nor clobber its channel/ref with stale re-delivery data.
func TestProviderMessageStore_UpsertConflictIsANoOp(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProviderMessageStore(pool)
	convID := testsupport.SeedConversation(t, pool)
	memberID := testsupport.SeedMember(t, pool)

	first := model.ProviderMessage{
		ID:             uuid.New(),
		ConversationID: convID,
		MemberID:       memberID,
		ProviderKind:   "slack",
		ChannelID:      "DFIRST",
		MessageRef:     "1111111111.000001",
		State:          model.ProviderMessageStatePosted,
	}

	if err := store.Upsert(t.Context(), first); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	// A second upsert for the same (conversation, member) pair — as a
	// replayed CONVERSATION_OPENED would produce — carries a different id,
	// channel/ref, and an advanced state (as if the row had been claimed in
	// between). None of it must land: the conflict is a pure no-op.
	second := first
	second.ID = uuid.New()
	second.ChannelID = "DSECOND"
	second.MessageRef = "2222222222.000002"
	second.State = model.ProviderMessageStateClaimedWon

	if err := store.Upsert(t.Context(), second); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	rows, err := store.ByConversation(t.Context(), convID)
	if err != nil {
		t.Fatalf("ByConversation: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("want exactly 1 row after a repeat upsert for the same pair, got %d", len(rows))
	}

	if rows[0].ID != first.ID || rows[0].ChannelID != "DFIRST" || rows[0].State != model.ProviderMessageStatePosted {
		t.Errorf("row = %+v, want the FIRST upsert's values preserved (conflict must be a no-op)", rows[0])
	}
}

// TestProviderMessageStore_UpsertConflictNeverRegressesAdvancedState proves
// the no-op conflict guard specifically for a row already past "posted": a
// duplicate OPEN arriving after a claim must leave claimed_won/claimed_lost
// untouched, never resetting the projector's state guard.
func TestProviderMessageStore_UpsertConflictNeverRegressesAdvancedState(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProviderMessageStore(pool)
	convID := testsupport.SeedConversation(t, pool)
	memberID := testsupport.SeedMember(t, pool)

	id := uuid.New()
	if err := store.Upsert(t.Context(), model.ProviderMessage{
		ID:             id,
		ConversationID: convID,
		MemberID:       memberID,
		ProviderKind:   "slack",
		ChannelID:      "DCLAIMED",
		MessageRef:     "5555555555.000001",
		State:          model.ProviderMessageStatePosted,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := store.SetState(t.Context(), id, model.ProviderMessageStateClaimedWon); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	// A duplicate OPEN replay attempts the same insert again (a fresh id, as
	// deliverToCandidate would mint) — it must not resurrect the row.
	if err := store.Upsert(t.Context(), model.ProviderMessage{
		ID:             uuid.New(),
		ConversationID: convID,
		MemberID:       memberID,
		ProviderKind:   "slack",
		ChannelID:      "DREPLAY",
		MessageRef:     "6666666666.000002",
		State:          model.ProviderMessageStatePosted,
	}); err != nil {
		t.Fatalf("replay Upsert: %v", err)
	}

	rows, err := store.ByConversation(t.Context(), convID)
	if err != nil {
		t.Fatalf("ByConversation: %v", err)
	}

	if len(rows) != 1 || rows[0].State != model.ProviderMessageStateClaimedWon {
		t.Errorf("rows = %+v, want 1 row still in claimed_won after a duplicate OPEN replay", rows)
	}
}

// TestProviderMessageStore_ByProviderRef proves the inbound-correlation
// lookup by (channel, message ref).
func TestProviderMessageStore_ByProviderRef(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProviderMessageStore(pool)
	convID := testsupport.SeedConversation(t, pool)
	memberID := testsupport.SeedMember(t, pool)

	if err := store.Upsert(t.Context(), model.ProviderMessage{
		ID:             uuid.New(),
		ConversationID: convID,
		MemberID:       memberID,
		ProviderKind:   "slack",
		ChannelID:      "DBYREFCHANNEL",
		MessageRef:     "3333333333.000001",
		State:          model.ProviderMessageStatePosted,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := store.ByProviderRef(t.Context(), "DBYREFCHANNEL", "3333333333.000001")
	if err != nil {
		t.Fatalf("ByProviderRef: %v", err)
	}

	if got.ConversationID != convID || got.MemberID != memberID {
		t.Errorf("got = %+v, want conversation %v member %v", got, convID, memberID)
	}

	if _, err := store.ByProviderRef(t.Context(), "DUNKNOWN", "0.0"); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("unknown ref: got %v, want ErrNotFound", err)
	}
}

// TestProviderMessageStore_LatestByChannel proves the channel-only inbound
// correlation used by providers with no stable per-message reference (Teams):
// it picks the most recently updated still-open row and ignores resolved/
// failed/released rows.
func TestProviderMessageStore_LatestByChannel(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProviderMessageStore(pool)
	convOld := testsupport.SeedConversation(t, pool)
	convNew := testsupport.SeedConversation(t, pool)
	convResolved := testsupport.SeedConversation(t, pool)
	memberID := testsupport.SeedMember(t, pool)

	channel := "TEAMS-CONV-" + uuid.NewString()

	if err := store.Upsert(t.Context(), model.ProviderMessage{
		ID:             uuid.New(),
		ConversationID: convOld,
		MemberID:       memberID,
		ProviderKind:   "teams",
		ChannelID:      channel,
		MessageRef:     "activity-old",
		State:          model.ProviderMessageStatePosted,
	}); err != nil {
		t.Fatalf("Upsert(old): %v", err)
	}

	// A resolved row on the SAME channel must not shadow the still-open one
	// below — LatestByChannel filters to posted/claimed_won.
	resolvedMemberID := testsupport.SeedMember(t, pool)
	if err := store.Upsert(t.Context(), model.ProviderMessage{
		ID:             uuid.New(),
		ConversationID: convResolved,
		MemberID:       resolvedMemberID,
		ProviderKind:   "teams",
		ChannelID:      channel,
		MessageRef:     "activity-resolved",
		State:          model.ProviderMessageStateResolved,
	}); err != nil {
		t.Fatalf("Upsert(resolved): %v", err)
	}

	newMemberID := testsupport.SeedMember(t, pool)
	if err := store.Upsert(t.Context(), model.ProviderMessage{
		ID:             uuid.New(),
		ConversationID: convNew,
		MemberID:       newMemberID,
		ProviderKind:   "teams",
		ChannelID:      channel,
		MessageRef:     "activity-new",
		State:          model.ProviderMessageStateClaimedWon,
	}); err != nil {
		t.Fatalf("Upsert(new): %v", err)
	}

	got, err := store.LatestByChannel(t.Context(), channel)
	if err != nil {
		t.Fatalf("LatestByChannel: %v", err)
	}

	if got.ConversationID != convNew {
		t.Errorf("LatestByChannel: got conversation %v, want the newest open row %v", got.ConversationID, convNew)
	}

	if _, err := store.LatestByChannel(t.Context(), "unknown-channel"); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("unknown channel: got %v, want ErrNotFound", err)
	}
}

// TestProviderMessageStore_SetState proves a state transition persists and
// that a non-existent id reports ErrNotFound.
func TestProviderMessageStore_SetState(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProviderMessageStore(pool)
	convID := testsupport.SeedConversation(t, pool)
	memberID := testsupport.SeedMember(t, pool)

	id := uuid.New()
	if err := store.Upsert(t.Context(), model.ProviderMessage{
		ID:             id,
		ConversationID: convID,
		MemberID:       memberID,
		ProviderKind:   "slack",
		ChannelID:      "DSETSTATECHANNEL",
		MessageRef:     "4444444444.000001",
		State:          model.ProviderMessageStatePosted,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := store.SetState(t.Context(), id, model.ProviderMessageStateClaimedLost); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	rows, err := store.ByConversation(t.Context(), convID)
	if err != nil {
		t.Fatalf("ByConversation: %v", err)
	}

	if len(rows) != 1 || rows[0].State != model.ProviderMessageStateClaimedLost {
		t.Errorf("rows = %+v, want 1 row in state claimed_lost", rows)
	}

	if err := store.SetState(t.Context(), uuid.New(), model.ProviderMessageStateFailed); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("unknown id: got %v, want ErrNotFound", err)
	}
}

// TestProviderMessageStore_Finalize proves the reserve→deliver→finalize
// sequence's storage half: a row written as "reserving" (no channel/ref yet)
// is finalized in one write with the channel/ref Deliver actually returned
// and its target state.
func TestProviderMessageStore_Finalize(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProviderMessageStore(pool)
	convID := testsupport.SeedConversation(t, pool)
	memberID := testsupport.SeedMember(t, pool)

	id := uuid.New()
	if err := store.Upsert(t.Context(), model.ProviderMessage{
		ID:             id,
		ConversationID: convID,
		MemberID:       memberID,
		ProviderKind:   "slack",
		ChannelID:      "",
		MessageRef:     "",
		State:          model.ProviderMessageStateReserving,
	}); err != nil {
		t.Fatalf("Upsert (reserve): %v", err)
	}

	if err := store.Finalize(t.Context(), id, "DFINALCHANNEL", "7777777777.000001", model.ProviderMessageStatePosted); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	rows, err := store.ByConversation(t.Context(), convID)
	if err != nil {
		t.Fatalf("ByConversation: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}

	if rows[0].State != model.ProviderMessageStatePosted || rows[0].ChannelID != "DFINALCHANNEL" || rows[0].MessageRef != "7777777777.000001" {
		t.Errorf("row = %+v, want posted/DFINALCHANNEL/7777777777.000001", rows[0])
	}
}

// TestProviderMessageStore_FinalizeGuardsOnReservingState proves Finalize
// never regresses a row that already moved past "reserving": the WHERE
// state = 'reserving' guard makes a second Finalize call on an already-
// finalized (or otherwise advanced) row a no-op that reports ErrNotFound,
// never a silent overwrite.
func TestProviderMessageStore_FinalizeGuardsOnReservingState(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProviderMessageStore(pool)
	convID := testsupport.SeedConversation(t, pool)
	memberID := testsupport.SeedMember(t, pool)

	id := uuid.New()
	if err := store.Upsert(t.Context(), model.ProviderMessage{
		ID:             id,
		ConversationID: convID,
		MemberID:       memberID,
		ProviderKind:   "slack",
		ChannelID:      "DALREADYPOSTED",
		MessageRef:     "8888888888.000001",
		State:          model.ProviderMessageStatePosted,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := store.Finalize(t.Context(), id, "DSHOULDNOTLAND", "9999999999.000002", model.ProviderMessageStatePosted); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("finalize on a non-reserving row: got %v, want ErrNotFound", err)
	}

	rows, err := store.ByConversation(t.Context(), convID)
	if err != nil {
		t.Fatalf("ByConversation: %v", err)
	}

	if len(rows) != 1 || rows[0].ChannelID != "DALREADYPOSTED" || rows[0].MessageRef != "8888888888.000001" {
		t.Errorf("row = %+v, want the original channel/ref untouched", rows[0])
	}

	if err := store.Finalize(t.Context(), uuid.New(), "DUNKNOWN", "0.0", model.ProviderMessageStatePosted); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("unknown id: got %v, want ErrNotFound", err)
	}
}
