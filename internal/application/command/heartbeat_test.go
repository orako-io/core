// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"testing"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
)

func TestHeartbeat_UpsertAndEvent(t *testing.T) {
	t.Parallel()

	presenceRepo := newFakePresenceRepo()
	bus := &fakeEventBus{}
	h := MustNewHeartbeatHandler(presenceRepo, bus)

	memberID := uuid.New()
	projectID := uuid.New()

	if err := h.Handle(t.Context(), HeartbeatCommand{
		MemberID:  memberID,
		ProjectID: projectID,
		Online:    true,
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// Presence upserted.
	p, ok := presenceRepo.records[memberID]
	if !ok {
		t.Fatal("presence not written")
	}

	if !p.Online {
		t.Error("Online = false, want true")
	}

	// PresenceChanged event emitted.
	env, ok := bus.lastOfType(orakov1.EventType_EVENT_TYPE_PRESENCE_CHANGED)
	if !ok {
		t.Fatal("PresenceChanged event not published")
	}

	pc := env.GetPresenceChanged()
	if pc == nil {
		t.Fatal("PresenceChanged payload is nil")
	}

	if pc.MemberId != memberID.String() {
		t.Errorf("MemberId = %q, want %q", pc.MemberId, memberID)
	}

	if !pc.Online {
		t.Error("PresenceChanged.Online = false, want true")
	}
}
