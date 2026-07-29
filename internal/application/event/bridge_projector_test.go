// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

// ── ledger fake ──────────────────────────────────────────────────────────────

// fakeBridgeLedger is an in-memory providerMessageLedger: ByConversation
// returns the seeded rows, SetState mutates them in place and records every
// call for assertions.
type fakeBridgeLedger struct {
	rows      []model.ProviderMessage
	setStates []model.ProviderMessageState // in call order
	byConvErr error

	// setStateFailFor, when non-empty, makes SetState fail (consuming one
	// count per matching call) for a specific (row id, target state) pair —
	// the shape the reserve→deliver→finalize tests need to force a failure
	// specifically at ONE row's finalize step (SetState(resolved)) while
	// letting every other SetState call — the reserve transition, or another
	// row's finalize into the very same state — succeed.
	setStateFailFor map[stateFailKey]int
}

// stateFailKey identifies one (row id, target state) SetState transition —
// see fakeBridgeLedger.setStateFailFor.
type stateFailKey struct {
	id    uuid.UUID
	state model.ProviderMessageState
}

func (f *fakeBridgeLedger) ByConversation(_ context.Context, _ uuid.UUID) ([]model.ProviderMessage, error) {
	if f.byConvErr != nil {
		return nil, f.byConvErr
	}

	return f.rows, nil
}

func (f *fakeBridgeLedger) SetState(_ context.Context, id uuid.UUID, state model.ProviderMessageState) error {
	key := stateFailKey{id: id, state: state}
	if f.setStateFailFor != nil && f.setStateFailFor[key] > 0 {
		f.setStateFailFor[key]--

		return fmt.Errorf("fakeBridgeLedger: forced SetState(%s, %s) failure", id, state)
	}

	f.setStates = append(f.setStates, state)

	for i, r := range f.rows {
		if r.ID == id {
			f.rows[i].State = state
		}
	}

	return nil
}

func (f *fakeBridgeLedger) stateOf(t *testing.T, id uuid.UUID) model.ProviderMessageState {
	t.Helper()

	for _, r := range f.rows {
		if r.ID == id {
			return r.State
		}
	}

	t.Fatalf("no ledger row %s", id)

	return ""
}

// ── envelope builders ────────────────────────────────────────────────────────

func claimedMsg(t *testing.T, projectID, conversationID, memberID uuid.UUID) *message.Message {
	t.Helper()

	env := &orakov1.Envelope{
		ProjectId: projectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_CONVERSATION_CLAIMED,
		Payload: &orakov1.Envelope_ConversationClaimed{
			ConversationClaimed: &orakov1.ConversationClaimed{
				ConversationId: conversationID.String(),
				MemberId:       memberID.String(),
			},
		},
	}

	return mustEnvelopeMsg(t, env)
}

func releasedMsg(t *testing.T, projectID, conversationID, releasedMemberID uuid.UUID) *message.Message {
	t.Helper()

	env := &orakov1.Envelope{
		ProjectId: projectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_CONVERSATION_RELEASED,
		Payload: &orakov1.Envelope_ConversationReleased{
			ConversationReleased: &orakov1.ConversationReleased{
				ConversationId:   conversationID.String(),
				ReleasedMemberId: releasedMemberID.String(),
				Reason:           "silence",
			},
		},
	}

	return mustEnvelopeMsg(t, env)
}

func bridgeClosedMsg(t *testing.T, projectID, conversationID, targetID uuid.UUID, resolution string) *message.Message {
	t.Helper()

	env := &orakov1.Envelope{
		ProjectId: projectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_CONVERSATION_CLOSED,
		Payload: &orakov1.Envelope_ConversationClosed{
			ConversationClosed: &orakov1.ConversationClosed{
				ConversationId:    conversationID.String(),
				Resolution:        resolution,
				KbEntryId:         uuid.NewString(),
				CloserMemberId:    uuid.NewString(),
				ResponderMemberId: targetID.String(),
			},
		},
	}

	return mustEnvelopeMsg(t, env)
}

func mustEnvelopeMsg(t *testing.T, env *orakov1.Envelope) *message.Message {
	t.Helper()

	payload, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	return message.NewMessage(uuid.NewString(), payload)
}

// ── CLAIMED / RELEASED replays are no-ops ────────────────────────────────────

// TestBridgeProjector_ClaimReleaseReplaysAreNoOps proves the acceptance
// criterion "a redelivered CLAIMED event (old log replays) is a no-op": the
// claim model is gone, so a CLAIMED or RELEASED envelope replayed from an old
// log must return nil without a single provider call or ledger state change.
func TestBridgeProjector_ClaimReleaseReplaysAreNoOps(t *testing.T) {
	t.Parallel()

	projectID, conversationID := uuid.New(), uuid.New()
	memberID := uuid.New()

	row := model.ProviderMessage{ID: uuid.New(), ConversationID: conversationID, MemberID: memberID, ChannelID: "C1", MessageRef: "M1", State: model.ProviderMessageStatePosted}
	ledger := &fakeBridgeLedger{rows: []model.ProviderMessage{row}}

	prov := &fakeEditorProvider{}
	resolvers := &fakeMemberProviders{byMember: map[uuid.UUID]service.Provider{memberID: prov}}

	h := BridgeProjector(ledger, resolvers, newFakeMemberBindingWriter(), nil, quietLogger())

	if err := h(claimedMsg(t, projectID, conversationID, memberID)); err != nil {
		t.Fatalf("replayed CLAIMED must be a no-op, got error: %v", err)
	}

	if err := h(releasedMsg(t, projectID, conversationID, memberID)); err != nil {
		t.Fatalf("replayed RELEASED must be a no-op, got error: %v", err)
	}

	if prov.deliverAttempts != 0 || prov.editAttempts != 0 {
		t.Errorf("replayed claim/release events must make zero provider calls, got deliver=%d edit=%d",
			prov.deliverAttempts, prov.editAttempts)
	}

	if len(ledger.setStates) != 0 {
		t.Errorf("replayed claim/release events must not touch the ledger, got SetState calls %v", ledger.setStates)
	}

	if ledger.stateOf(t, row.ID) != model.ProviderMessageStatePosted {
		t.Errorf("row state = %q, want untouched posted", ledger.stateOf(t, row.ID))
	}
}

// ── CLOSED ───────────────────────────────────────────────────────────────────

// TestBridgeProjector_Closed_NonExpertsGetResolutionContent proves every
// row still in play — except the labeled responder's — receives the
// resolution content (state resolved) with the original DM edited to a short
// resolved notice, the responder's row is left untouched, and a row that
// never delivered (state failed) is skipped. A duplicate CLOSED event makes
// zero further calls.
func TestBridgeProjector_Closed_NonExpertsGetResolutionContent(t *testing.T) {
	t.Parallel()

	projectID, conversationID := uuid.New(), uuid.New()
	targetID, otherID, failedMemberID := uuid.New(), uuid.New(), uuid.New()

	specialistRow := model.ProviderMessage{ID: uuid.New(), ConversationID: conversationID, MemberID: targetID, ChannelID: "C1", MessageRef: "M1", State: model.ProviderMessageStatePosted}
	otherRow := model.ProviderMessage{ID: uuid.New(), ConversationID: conversationID, MemberID: otherID, ChannelID: "C2", MessageRef: "M2", State: model.ProviderMessageStatePosted}
	failedRow := model.ProviderMessage{ID: uuid.New(), ConversationID: conversationID, MemberID: failedMemberID, ChannelID: "C3", MessageRef: "M3", State: model.ProviderMessageStateFailed}

	ledger := &fakeBridgeLedger{rows: []model.ProviderMessage{specialistRow, otherRow, failedRow}}

	specialistProv := &fakeEditorProvider{}
	otherProv := &fakeEditorProvider{}
	failedProv := &fakeEditorProvider{}

	resolvers := &fakeMemberProviders{byMember: map[uuid.UUID]service.Provider{
		targetID:       specialistProv,
		otherID:        otherProv,
		failedMemberID: failedProv,
	}}

	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: targetID, DisplayName: "Winnie"})

	h := BridgeProjector(ledger, resolvers, members, nil, quietLogger())

	if err := h(bridgeClosedMsg(t, projectID, conversationID, targetID, "Use a partial index.")); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if len(specialistProv.delivered) != 0 || len(specialistProv.edited) != 0 {
		t.Errorf("the responder must get nothing new, got delivered=%d edited=%d", len(specialistProv.delivered), len(specialistProv.edited))
	}

	if ledger.stateOf(t, specialistRow.ID) != model.ProviderMessageStatePosted {
		t.Errorf("responder row state must be untouched, got %q", ledger.stateOf(t, specialistRow.ID))
	}

	if len(otherProv.delivered) != 1 {
		t.Fatalf("want 1 closure Deliver to the non-responder, got %d", len(otherProv.delivered))
	}

	closure := otherProv.delivered[0]
	if closure.Kind != service.MessageKindClosure {
		t.Errorf("Kind = %q, want closure", closure.Kind)
	}

	wantPrefix := "✅ Resolved by Winnie:"
	if !strings.HasPrefix(closure.Question, wantPrefix) {
		t.Errorf("closure content = %q, want prefix %q", closure.Question, wantPrefix)
	}

	if !strings.Contains(closure.Question, "Use a partial index.") {
		t.Errorf("closure content must carry the resolution, got %q", closure.Question)
	}

	if len(otherProv.edited) != 1 {
		t.Fatalf("want the non-responder's original DM edited to a resolved notice, got %d edits", len(otherProv.edited))
	}

	if got := otherProv.edited[0].text; got != "✅ Resolved — see the message above." {
		t.Errorf("resolved-notice edit text = %q", got)
	}

	if ledger.stateOf(t, otherRow.ID) != model.ProviderMessageStateResolved {
		t.Errorf("non-responder row state = %q, want resolved", ledger.stateOf(t, otherRow.ID))
	}

	if len(failedProv.delivered) != 0 {
		t.Errorf("a failed-state row must be skipped, got %d delivers", len(failedProv.delivered))
	}

	if ledger.stateOf(t, failedRow.ID) != model.ProviderMessageStateFailed {
		t.Errorf("failed row state must be untouched, got %q", ledger.stateOf(t, failedRow.ID))
	}

	// Duplicate CLOSED event: every touched row already left its prior state.
	if err := h(bridgeClosedMsg(t, projectID, conversationID, targetID, "Use a partial index.")); err != nil {
		t.Fatalf("handler (duplicate) returned error: %v", err)
	}

	if len(otherProv.delivered) != 1 {
		t.Errorf("duplicate CLOSED must make zero further calls, got %d delivers", len(otherProv.delivered))
	}
}

// TestBridgeProjector_Closed_NoEditorProviderDeliverCarriesTheNotice proves a
// Telegram-shaped provider (no Editor) never gets an Edit call — structurally
// impossible, it has no such method — and its closure notice is entirely
// carried by the closure Deliver; the row still finalizes to resolved.
func TestBridgeProjector_Closed_NoEditorProviderDeliverCarriesTheNotice(t *testing.T) {
	t.Parallel()

	projectID, conversationID := uuid.New(), uuid.New()
	targetID, otherID := uuid.New(), uuid.New()

	otherRow := model.ProviderMessage{ID: uuid.New(), ConversationID: conversationID, MemberID: otherID, ChannelID: "C2", MessageRef: "M2", State: model.ProviderMessageStatePosted}
	ledger := &fakeBridgeLedger{rows: []model.ProviderMessage{otherRow}}

	otherProv := &fakeNudgeOnlyProvider{}
	resolvers := &fakeMemberProviders{byMember: map[uuid.UUID]service.Provider{otherID: otherProv}}

	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: targetID, DisplayName: "Winnie"})

	h := BridgeProjector(ledger, resolvers, members, nil, quietLogger())

	if err := h(bridgeClosedMsg(t, projectID, conversationID, targetID, "Use a partial index.")); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if len(otherProv.delivered) != 1 {
		t.Fatalf("want 1 closure Deliver, got %d", len(otherProv.delivered))
	}

	got := otherProv.delivered[0]
	if got.Kind != service.MessageKindClosure {
		t.Errorf("Kind = %q, want closure", got.Kind)
	}

	if !strings.Contains(got.Question, "Use a partial index.") {
		t.Errorf("closure content must carry the resolution, got %q", got.Question)
	}

	if ledger.stateOf(t, otherRow.ID) != model.ProviderMessageStateResolved {
		t.Errorf("row state = %q, want resolved", ledger.stateOf(t, otherRow.ID))
	}
}

// ── store-level failure ──────────────────────────────────────────────────────

// TestBridgeProjector_LedgerFailureRetries proves a store-level failure
// (resolving the ledger rows themselves) returns an error so the router
// retries the whole message.
func TestBridgeProjector_LedgerFailureRetries(t *testing.T) {
	t.Parallel()

	ledger := &fakeBridgeLedger{byConvErr: context.DeadlineExceeded}
	members := newFakeMemberBindingWriter()

	h := BridgeProjector(ledger, &fakeMemberProviders{}, members, nil, quietLogger())

	if err := h(bridgeClosedMsg(t, uuid.New(), uuid.New(), uuid.New(), "resolved.")); err == nil {
		t.Fatal("want an error on ledger store failure, got nil")
	}
}

// ── reserve→deliver→finalize: the CLOSED resolution delivery must never
// re-Deliver on a retry that lands after a successful Deliver but a failed
// finalize write. ────────────────────────────────────────────────────────────

// TestBridgeProjector_Closed_ResolutionFinalizeFailureRetriesWithoutDuplicateSend
// proves the reserve→deliver→finalize sequence: Deliver succeeds, the final
// SetState(resolved) fails, and the retry must skip the row (now "reserving")
// via projectClosed's guard rather than re-Deliver the closure content. The
// row is left "reserving" (the accepted crash-window tradeoff).
func TestBridgeProjector_Closed_ResolutionFinalizeFailureRetriesWithoutDuplicateSend(t *testing.T) {
	t.Parallel()

	projectID, conversationID := uuid.New(), uuid.New()
	targetID, otherID := uuid.New(), uuid.New()

	specialistRow := model.ProviderMessage{ID: uuid.New(), ConversationID: conversationID, MemberID: targetID, ChannelID: "C1", MessageRef: "M1", State: model.ProviderMessageStatePosted}
	otherRow := model.ProviderMessage{ID: uuid.New(), ConversationID: conversationID, MemberID: otherID, ChannelID: "C2", MessageRef: "M2", State: model.ProviderMessageStatePosted}

	ledger := &fakeBridgeLedger{
		rows:            []model.ProviderMessage{specialistRow, otherRow},
		setStateFailFor: map[stateFailKey]int{{id: otherRow.ID, state: model.ProviderMessageStateResolved}: 1},
	}

	specialistProv := &fakeEditorProvider{}
	otherProv := &fakeEditorProvider{}

	resolvers := &fakeMemberProviders{byMember: map[uuid.UUID]service.Provider{
		targetID: specialistProv,
		otherID:  otherProv,
	}}

	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: targetID, DisplayName: "Winnie"})

	h := BridgeProjector(ledger, resolvers, members, nil, quietLogger())

	if err := h(bridgeClosedMsg(t, projectID, conversationID, targetID, "Use a partial index.")); err == nil {
		t.Fatal("want an error when the closure finalize fails, got nil")
	}

	if otherProv.deliverAttempts != 1 || len(otherProv.delivered) != 1 {
		t.Fatalf("want Deliver called exactly once after the first pass, got attempts=%d delivered=%d",
			otherProv.deliverAttempts, len(otherProv.delivered))
	}

	if ledger.stateOf(t, otherRow.ID) != model.ProviderMessageStateReserving {
		t.Fatalf("row state = %q, want reserving (finalize never landed)", ledger.stateOf(t, otherRow.ID))
	}

	if err := h(bridgeClosedMsg(t, projectID, conversationID, targetID, "Use a partial index.")); err != nil {
		t.Fatalf("retry returned error: %v", err)
	}

	if otherProv.deliverAttempts != 1 {
		t.Errorf("want Deliver still called exactly once net after the retry (no duplicate), got %d attempts", otherProv.deliverAttempts)
	}

	if ledger.stateOf(t, otherRow.ID) != model.ProviderMessageStateReserving {
		t.Errorf("row state = %q, want it to stay reserving (the accepted crash-window tradeoff)", ledger.stateOf(t, otherRow.ID))
	}
}

// TestBridgeProjector_Closed_EditFailureAfterDeliverNeverRedelivers is the
// Edit-step counterpart: Deliver succeeds, the resolved-notice Edit fails.
// The reservation must NOT be reverted (something was sent), so the retry
// skips the "reserving" row instead of re-Delivering the closure content.
func TestBridgeProjector_Closed_EditFailureAfterDeliverNeverRedelivers(t *testing.T) {
	t.Parallel()

	projectID, conversationID := uuid.New(), uuid.New()
	targetID, otherID := uuid.New(), uuid.New()

	otherRow := model.ProviderMessage{ID: uuid.New(), ConversationID: conversationID, MemberID: otherID, ChannelID: "C2", MessageRef: "M2", State: model.ProviderMessageStatePosted}
	ledger := &fakeBridgeLedger{rows: []model.ProviderMessage{otherRow}}

	otherProv := &fakeEditorProvider{editFailTimes: 1}
	resolvers := &fakeMemberProviders{byMember: map[uuid.UUID]service.Provider{otherID: otherProv}}

	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: targetID, DisplayName: "Winnie"})

	h := BridgeProjector(ledger, resolvers, members, nil, quietLogger())

	if err := h(bridgeClosedMsg(t, projectID, conversationID, targetID, "Use a partial index.")); err == nil {
		t.Fatal("want an error when the resolved-notice Edit fails, got nil")
	}

	if otherProv.deliverAttempts != 1 || otherProv.editAttempts != 1 {
		t.Fatalf("want exactly one Deliver and one Edit attempt, got deliver=%d edit=%d",
			otherProv.deliverAttempts, otherProv.editAttempts)
	}

	if ledger.stateOf(t, otherRow.ID) != model.ProviderMessageStateReserving {
		t.Fatalf("row state = %q, want reserving (Deliver landed, finalize didn't)", ledger.stateOf(t, otherRow.ID))
	}

	if err := h(bridgeClosedMsg(t, projectID, conversationID, targetID, "Use a partial index.")); err != nil {
		t.Fatalf("retry returned error: %v", err)
	}

	if otherProv.deliverAttempts != 1 {
		t.Errorf("want Deliver still called exactly once net after the retry (no duplicate), got %d attempts", otherProv.deliverAttempts)
	}
}

// TestBridgeProjector_Closed_ResolutionDeliverFailureRevertsAndRetries proves
// the revert path: a Deliver failure (nothing sent) restores the row's prior
// state so the next retry actually retries Deliver.
func TestBridgeProjector_Closed_ResolutionDeliverFailureRevertsAndRetries(t *testing.T) {
	t.Parallel()

	projectID, conversationID := uuid.New(), uuid.New()
	targetID, otherID := uuid.New(), uuid.New()

	specialistRow := model.ProviderMessage{ID: uuid.New(), ConversationID: conversationID, MemberID: targetID, ChannelID: "C1", MessageRef: "M1", State: model.ProviderMessageStatePosted}
	otherRow := model.ProviderMessage{ID: uuid.New(), ConversationID: conversationID, MemberID: otherID, ChannelID: "C2", MessageRef: "M2", State: model.ProviderMessageStatePosted}

	ledger := &fakeBridgeLedger{rows: []model.ProviderMessage{specialistRow, otherRow}}

	specialistProv := &fakeEditorProvider{}
	otherProv := &fakeEditorProvider{deliverFailTimes: 1}

	resolvers := &fakeMemberProviders{byMember: map[uuid.UUID]service.Provider{
		targetID: specialistProv,
		otherID:  otherProv,
	}}

	members := newFakeMemberBindingWriter()
	members.add(model.Member{ID: targetID, DisplayName: "Winnie"})

	h := BridgeProjector(ledger, resolvers, members, nil, quietLogger())

	if err := h(bridgeClosedMsg(t, projectID, conversationID, targetID, "Use a partial index.")); err == nil {
		t.Fatal("want an error when Deliver fails, got nil")
	}

	if len(otherProv.delivered) != 0 {
		t.Fatalf("want nothing delivered after a failed Deliver, got %d", len(otherProv.delivered))
	}

	if ledger.stateOf(t, otherRow.ID) != model.ProviderMessageStatePosted {
		t.Fatalf("row state = %q, want reverted to posted (nothing was sent)", ledger.stateOf(t, otherRow.ID))
	}

	if err := h(bridgeClosedMsg(t, projectID, conversationID, targetID, "Use a partial index.")); err != nil {
		t.Fatalf("retry returned error: %v", err)
	}

	if otherProv.deliverAttempts != 2 || len(otherProv.delivered) != 1 {
		t.Errorf("want 2 Deliver attempts (1 failed + 1 succeeded) and exactly 1 net delivery, got attempts=%d delivered=%d",
			otherProv.deliverAttempts, len(otherProv.delivered))
	}

	if ledger.stateOf(t, otherRow.ID) != model.ProviderMessageStateResolved {
		t.Errorf("row state = %q, want resolved after the successful retry", ledger.stateOf(t, otherRow.ID))
	}
}
