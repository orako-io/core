// SPDX-License-Identifier: AGPL-3.0-or-later

package eventlog

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/goleak"

	"github.com/orako-io/core/internal/application/domain/model"
)

// TestMain asserts the package leaves no stray goroutines after each test.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// minimalPayload returns a non-empty JSONB payload for event tests.
func minimalPayload(t *testing.T, content map[string]any) []byte {
	t.Helper()

	b, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("minimalPayload: %v", err)
	}

	return b
}

// newTestEvent builds a valid Event for the given projectID, ready to Append.
func newTestEvent(t *testing.T, projectID uuid.UUID, seq int) model.Event {
	t.Helper()

	ev, err := model.NewEvent(
		uuid.New(),
		projectID,
		model.EventTypePresenceChanged,
		minimalPayload(t, map[string]any{"seq": seq}),
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("newTestEvent: %v", err)
	}

	return ev
}
