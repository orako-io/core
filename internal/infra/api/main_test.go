// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs goleak after all tests to catch goroutine leaks.
// The watermill GoChannel bus goroutines are created and torn down within
// tests; we do not ignore any functions here because we use no external bus.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
