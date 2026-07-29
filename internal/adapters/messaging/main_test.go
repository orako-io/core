// SPDX-License-Identifier: AGPL-3.0-or-later

package messaging_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain asserts the package leaves no stray goroutines once every test and
// its cleanups have run — the goleak gate for the event router and the
// Postgres clients starting and stopping cleanly.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
