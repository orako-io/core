// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// fakeSeatGate is a SeatGate whose verdict is fixed.
type fakeSeatGate struct {
	err    error
	called bool
}

func (g *fakeSeatGate) AllowNewMember(_ context.Context, _ uuid.UUID) error {
	g.called = true
	return g.err
}

func TestAddMemberBlockedBySeatGate(t *testing.T) {
	t.Parallel()

	writer := newFakeMemberWriter()
	bus := &fakeEventBus{}
	gate := &fakeSeatGate{err: errs.InvalidError{Field: "seats", Reason: "seat limit reached"}}
	h := MustNewAddMemberHandler(writer, fakeTransactor{}, &updateMemberFakeStore{member: baseMember()}, bus, gate, nil)

	_, err := h.Handle(t.Context(), AddMemberCommand{
		ProjectID:   uuid.New(),
		Email:       "blocked@example.com",
		DisplayName: "Blocked",
	})
	if err == nil {
		t.Fatal("Handle = nil, want the gate's error")
	}

	if !gate.called {
		t.Error("gate was not consulted")
	}

	// The member must NOT be created when the gate blocks.
	if len(writer.members) != 0 {
		t.Errorf("writer stored %d members, want 0 (blocked before write)", len(writer.members))
	}

	// No lifecycle event emitted.
	if len(bus.published) != 0 {
		t.Errorf("bus published %d events despite the block, want 0", len(bus.published))
	}
}

func TestAddMemberAllowedBySeatGate(t *testing.T) {
	t.Parallel()

	writer := newFakeMemberWriter()
	bus := &fakeEventBus{}
	gate := &fakeSeatGate{err: nil}
	h := MustNewAddMemberHandler(writer, fakeTransactor{}, &updateMemberFakeStore{member: baseMember()}, bus, gate, nil)

	result, err := h.Handle(t.Context(), AddMemberCommand{
		ProjectID:   uuid.New(),
		Email:       "ok@example.com",
		DisplayName: "Okay",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if !gate.called {
		t.Error("gate was not consulted")
	}

	if result.MemberID == uuid.Nil {
		t.Error("MemberID is nil; member should have been created")
	}
}
