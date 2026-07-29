// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestOnlineNowDecays(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()

	fresh := Presence{MemberID: memberID, Online: true, UpdatedAt: time.Now()}
	if !fresh.OnlineNow() {
		t.Error("a fresh heartbeat must count as online")
	}

	stale := Presence{MemberID: memberID, Online: true, UpdatedAt: time.Now().Add(-PresenceFreshness - time.Second)}
	if stale.OnlineNow() {
		t.Error("a stale heartbeat must decay to offline, even with Online=true")
	}

	offline := Presence{MemberID: memberID, Online: false, UpdatedAt: time.Now()}
	if offline.OnlineNow() {
		t.Error("Online=false is offline regardless of freshness")
	}
}
