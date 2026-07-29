// SPDX-License-Identifier: AGPL-3.0-or-later

package conversation_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/conversation"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestConversationMembersStore_MergeParity proves the merged conversation_members
// table preserves the exact behavior of the old conversation_candidates /
// conversation_participants split: dispatch stamps invited_at, add_participant
// stamps added_at+added_by, exclude stamps excluded_at, a member dispatched AND
// explicitly added lives on ONE row carrying both column sets, and every read
// (ByConversation / IsActiveCandidate / ActiveCandidatesByConversations /
// ParticipantsByConversation(s) / OpenPoolFor) returns the same sets it did when
// the two tables were separate.
func TestConversationMembersStore_MergeParity(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	openStore := conversation.NewOpenConversationStore(pool)
	convStore := conversation.NewStore(pool)
	candStore := conversation.NewCandidateStore(pool)

	projectID := testsupport.SeedProject(t, pool)
	asker := testsupport.SeedMember(t, pool)
	mCand := testsupport.SeedMember(t, pool)     // dispatched only
	mBoth := testsupport.SeedMember(t, pool)     // dispatched AND explicitly added
	mExcluded := testsupport.SeedMember(t, pool) // dispatched then released (excluded)
	mPart := testsupport.SeedMember(t, pool)     // explicitly added only (never dispatched)

	convID := uuid.New()

	conv, err := model.NewConversation(convID, projectID, asker, "which index?")
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}

	msg, err := model.NewMessage(uuid.New(), convID, asker, model.MessageRoleQuestion, "which index?", model.MessageSourceHuman)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}

	// Dispatch: OpenConversation writes the candidate pool (invited_at).
	if err := openStore.OpenConversation(t.Context(), conv, msg, []uuid.UUID{mCand, mBoth, mExcluded}); err != nil {
		t.Fatalf("OpenConversation: %v", err)
	}

	// Explicit adds: mBoth (added by the asker) and mPart (auto fan-out, no adder).
	if err := convStore.AddParticipant(t.Context(), convID, mBoth, asker); err != nil {
		t.Fatalf("AddParticipant(mBoth): %v", err)
	}

	if err := convStore.AddParticipant(t.Context(), convID, mPart, uuid.Nil); err != nil {
		t.Fatalf("AddParticipant(mPart): %v", err)
	}

	// Exclude mExcluded from the pool.
	if err := candStore.Exclude(t.Context(), convID, mExcluded); err != nil {
		t.Fatalf("Exclude: %v", err)
	}

	// --- candidates (invited_at IS NOT NULL) ---------------------------------
	cands, err := candStore.ByConversation(t.Context(), convID)
	if err != nil {
		t.Fatalf("ByConversation: %v", err)
	}

	gotCands := map[uuid.UUID]model.Candidate{}
	for _, c := range cands {
		gotCands[c.MemberID] = c
	}

	if len(gotCands) != 3 {
		t.Fatalf("ByConversation: got %d candidates, want 3 (mCand, mBoth, mExcluded)", len(gotCands))
	}

	for _, id := range []uuid.UUID{mCand, mBoth, mExcluded} {
		c, ok := gotCands[id]
		if !ok {
			t.Fatalf("ByConversation missing candidate %v", id)
		}

		if c.InvitedAt.IsZero() {
			t.Errorf("candidate %v: InvitedAt is zero, want set", id)
		}
	}

	if _, ok := gotCands[mPart]; ok {
		t.Error("ByConversation must not return the pure participant mPart as a candidate")
	}

	if !gotCands[mCand].Active() || !gotCands[mBoth].Active() {
		t.Error("mCand and mBoth must be active candidates")
	}

	if gotCands[mExcluded].Active() {
		t.Error("mExcluded must be inactive (excluded_at set)")
	}

	// --- IsActiveCandidate ---------------------------------------------------
	for _, tc := range []struct {
		id   uuid.UUID
		want bool
		name string
	}{
		{mCand, true, "mCand"},
		{mBoth, true, "mBoth"},
		{mExcluded, false, "mExcluded (released)"},
		{mPart, false, "mPart (pure participant, never dispatched)"},
	} {
		got, err := candStore.IsActiveCandidate(t.Context(), convID, tc.id)
		if err != nil {
			t.Fatalf("IsActiveCandidate(%s): %v", tc.name, err)
		}

		if got != tc.want {
			t.Errorf("IsActiveCandidate(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}

	// --- ActiveCandidatesByConversations (batch) -----------------------------
	activeByConv, err := convStore.ActiveCandidatesByConversations(t.Context(), []uuid.UUID{convID})
	if err != nil {
		t.Fatalf("ActiveCandidatesByConversations: %v", err)
	}

	active := map[uuid.UUID]bool{}
	for _, id := range activeByConv[convID] {
		active[id] = true
	}

	if len(active) != 2 || !active[mCand] || !active[mBoth] {
		t.Errorf("ActiveCandidatesByConversations = %v, want {mCand, mBoth}", activeByConv[convID])
	}

	if active[mExcluded] || active[mPart] {
		t.Error("ActiveCandidatesByConversations must exclude the released and pure-participant members")
	}

	// --- participants (added_at IS NOT NULL) ---------------------------------
	parts, err := convStore.ParticipantsByConversation(t.Context(), convID)
	if err != nil {
		t.Fatalf("ParticipantsByConversation: %v", err)
	}

	gotParts := map[uuid.UUID]model.ConversationParticipant{}
	for _, p := range parts {
		gotParts[p.MemberID] = p
	}

	if len(gotParts) != 2 {
		t.Fatalf("ParticipantsByConversation: got %d, want 2 (mBoth, mPart)", len(gotParts))
	}

	if p, ok := gotParts[mBoth]; !ok || p.AddedBy != asker || p.AddedAt.IsZero() {
		t.Errorf("participant mBoth = %+v, want AddedBy=%v and AddedAt set", p, asker)
	}

	if p, ok := gotParts[mPart]; !ok || p.AddedBy != uuid.Nil || p.AddedAt.IsZero() {
		t.Errorf("participant mPart = %+v, want AddedBy=Nil (auto) and AddedAt set", p)
	}

	if _, ok := gotParts[mCand]; ok {
		t.Error("ParticipantsByConversation must not return the dispatch-only candidate mCand")
	}

	// Batch variant returns the same set.
	batch, err := convStore.ParticipantsByConversations(t.Context(), []uuid.UUID{convID})
	if err != nil {
		t.Fatalf("ParticipantsByConversations: %v", err)
	}

	if len(batch[convID]) != 2 {
		t.Errorf("ParticipantsByConversations = %v, want 2 rows", batch[convID])
	}

	// --- the "both" member is ONE row carrying both column sets --------------
	var rowCount int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM conversation_members WHERE conversation_id = $1 AND member_id = $2`,
		convID, mBoth,
	).Scan(&rowCount); err != nil {
		t.Fatalf("counting merged row: %v", err)
	}

	if rowCount != 1 {
		t.Fatalf("mBoth: got %d conversation_members rows, want exactly 1 (dispatch + add must merge)", rowCount)
	}

	var (
		hasInvited, hasAdded bool
		rowAddedBy           uuid.UUID
	)

	if err := pool.QueryRow(t.Context(),
		`SELECT invited_at IS NOT NULL, added_at IS NOT NULL, added_by
		 FROM conversation_members WHERE conversation_id = $1 AND member_id = $2`,
		convID, mBoth,
	).Scan(&hasInvited, &hasAdded, &rowAddedBy); err != nil {
		t.Fatalf("querying merged row: %v", err)
	}

	if !hasInvited || !hasAdded || rowAddedBy != asker {
		t.Errorf("mBoth merged row: invited=%v added=%v addedBy=%v, want true/true/%v",
			hasInvited, hasAdded, rowAddedBy, asker)
	}

	// --- AddParticipant idempotency: a re-add keeps the original added_by -----
	other := testsupport.SeedMember(t, pool)
	if err := convStore.AddParticipant(t.Context(), convID, mBoth, other); err != nil {
		t.Fatalf("AddParticipant(mBoth re-add): %v", err)
	}

	reParts, err := convStore.ParticipantsByConversation(t.Context(), convID)
	if err != nil {
		t.Fatalf("ParticipantsByConversation (after re-add): %v", err)
	}

	for _, p := range reParts {
		if p.MemberID == mBoth && p.AddedBy != asker {
			t.Errorf("re-add changed AddedBy to %v, want it to stay %v (idempotent)", p.AddedBy, asker)
		}
	}

	// --- OpenPoolFor: active candidates see the open pool conversation -------
	assertPool := func(member uuid.UUID, want bool, name string) {
		t.Helper()

		recs, err := candStore.OpenPoolFor(t.Context(), member)
		if err != nil {
			t.Fatalf("OpenPoolFor(%s): %v", name, err)
		}

		found := false
		for _, r := range recs {
			if r.ID == convID {
				found = true
				break
			}
		}

		if found != want {
			t.Errorf("OpenPoolFor(%s): conversation present=%v, want %v", name, found, want)
		}
	}

	assertPool(mCand, true, "mCand")          // active candidate → in pool inbox
	assertPool(mExcluded, false, "mExcluded") // released → not in pool
	assertPool(mPart, false, "mPart")         // pure participant → not a pool candidacy
}
