// SPDX-License-Identifier: AGPL-3.0-or-later

// Package slack provides Slack-specific HTTP handlers for the Orako server:
// request signature verification, the Events API inbound webhook (with the
// url_verification challenge handshake), and the OAuth install flow.
package slack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// replayWindow is the maximum accepted request-timestamp age (anti-replay);
// 5 minutes is Slack's recommended value.
const replayWindow = 5 * time.Minute

// sigVersion is the scheme prefix Slack prepends to all v0 request signatures.
const sigVersion = "v0"

// Sentinel errors returned by Verifier.Verify. All are safe to surface in HTTP
// responses; none carries secret material.
var (
	// ErrMissingSignature is returned when X-Slack-Signature is absent.
	ErrMissingSignature = errors.New("slack: missing X-Slack-Signature header")
	// ErrMissingTimestamp is returned when X-Slack-Request-Timestamp is absent.
	ErrMissingTimestamp = errors.New("slack: missing X-Slack-Request-Timestamp header")
	// ErrStaleTimestamp is returned when the request is outside the replay window.
	ErrStaleTimestamp = errors.New("slack: request timestamp outside replay window")
	// ErrInvalidSignature is returned when the HMAC does not match.
	ErrInvalidSignature = errors.New("slack: invalid request signature")
)

// Verifier validates Slack request signatures using the v0 HMAC-SHA256 scheme:
// HMAC-SHA256(key=signing_secret, msg="v0:{timestamp}:{raw_body}"), compared in
// constant time against X-Slack-Signature, plus a ±replayWindow timestamp check.
type Verifier struct {
	secret []byte
	now    func() time.Time // overridable in tests; defaults to time.Now
}

// NewVerifier builds a Verifier for the given Slack app signing secret
// (never an OAuth token).
func NewVerifier(signingSecret string) *Verifier {
	return &Verifier{
		secret: []byte(signingSecret),
		now:    time.Now,
	}
}

// SetNow overrides the Verifier's clock source; intended only for testing.
func (v *Verifier) SetNow(f func() time.Time) {
	v.now = f
}

// Verify checks that the Slack request is authentic and within the replay
// window. body must be the raw, unmodified request body. Returns one of the
// package sentinel errors on failure.
func (v *Verifier) Verify(rawSig, rawTS string, body []byte) error {
	if rawSig == "" {
		return ErrMissingSignature
	}

	if rawTS == "" {
		return ErrMissingTimestamp
	}

	ts, err := strconv.ParseInt(rawTS, 10, 64)
	if err != nil {
		// A malformed timestamp cannot be a legitimate Slack request.
		return fmt.Errorf("%w: malformed timestamp", ErrStaleTimestamp)
	}

	// Reject requests more than replayWindow old or from the future.
	age := v.now().Sub(time.Unix(ts, 0))
	if age > replayWindow || age < -replayWindow {
		return ErrStaleTimestamp
	}

	msg := fmt.Sprintf("%s:%s:%s", sigVersion, rawTS, body)
	mac := hmac.New(sha256.New, v.secret)
	mac.Write([]byte(msg))
	expected := sigVersion + "=" + hex.EncodeToString(mac.Sum(nil))

	// Constant-time comparison prevents timing attacks.
	if !hmac.Equal([]byte(expected), []byte(rawSig)) {
		return ErrInvalidSignature
	}

	return nil
}
