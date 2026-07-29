// SPDX-License-Identifier: AGPL-3.0-or-later

package slack_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"time"
)

// newTestLogger returns a no-op slog.Logger suitable for unit tests.
func newTestLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// signForTest computes the Slack v0 HMAC-SHA256 signature for body at the given
// wall-clock time and returns both the Unix timestamp string and the full
// "v0=<hex>" signature string.
//
// Use the returned values as X-Slack-Request-Timestamp and X-Slack-Signature
// headers respectively when constructing test requests.
func signForTest(secret string, body []byte, at time.Time) (tsStr, sig string) {
	tsStr = strconv.FormatInt(at.Unix(), 10)
	msg := fmt.Sprintf("v0:%s:%s", tsStr, body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	sig = "v0=" + hex.EncodeToString(mac.Sum(nil))

	return tsStr, sig
}
