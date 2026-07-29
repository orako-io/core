// SPDX-License-Identifier: AGPL-3.0-or-later

package slack_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	slackhttp "github.com/orako-io/core/internal/infra/api/slack"
)

// testSecret is the signing secret used in all signature tests.
const testSecret = "test_signing_secret_value"

// signBody computes the correct v0 signature for the given timestamp and body.
func signBody(t *testing.T, secret, rawTS string, body []byte) string {
	t.Helper()

	msg := fmt.Sprintf("v0:%s:%s", rawTS, body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))

	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

// TestVerifier_TableDriven exercises the full set of Verify outcomes.
func TestVerifier_TableDriven(t *testing.T) {
	t.Parallel()

	body := []byte(`{"type":"event_callback","event":{"type":"message","text":"hello"}}`)

	nowFixed := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	freshTSFixed := strconv.FormatInt(nowFixed.Unix(), 10)
	staleTSFixed := strconv.FormatInt(nowFixed.Add(-6*time.Minute).Unix(), 10) // 6 min old
	futureTSFixed := strconv.FormatInt(nowFixed.Add(6*time.Minute).Unix(), 10) // 6 min future

	validSig := signBody(t, testSecret, freshTSFixed, body)
	otherBody := []byte(`{"type":"event_callback","event":{"type":"message","text":"tampered"}}`)
	tamperedSig := signBody(t, testSecret, freshTSFixed, otherBody)

	cases := []struct {
		name    string
		rawSig  string
		rawTS   string
		body    []byte
		wantErr error
	}{
		{
			name:    "valid_signature_passes",
			rawSig:  validSig,
			rawTS:   freshTSFixed,
			body:    body,
			wantErr: nil,
		},
		{
			name:    "missing_signature_header",
			rawSig:  "",
			rawTS:   freshTSFixed,
			body:    body,
			wantErr: slackhttp.ErrMissingSignature,
		},
		{
			name:    "missing_timestamp_header",
			rawSig:  validSig,
			rawTS:   "",
			body:    body,
			wantErr: slackhttp.ErrMissingTimestamp,
		},
		{
			name:    "stale_timestamp_rejected_old",
			rawSig:  signBody(t, testSecret, staleTSFixed, body),
			rawTS:   staleTSFixed,
			body:    body,
			wantErr: slackhttp.ErrStaleTimestamp,
		},
		{
			name:    "stale_timestamp_rejected_future",
			rawSig:  signBody(t, testSecret, futureTSFixed, body),
			rawTS:   futureTSFixed,
			body:    body,
			wantErr: slackhttp.ErrStaleTimestamp,
		},
		{
			name:    "tampered_body_rejected",
			rawSig:  tamperedSig,
			rawTS:   freshTSFixed,
			body:    body, // original body, signature computed over tampered
			wantErr: slackhttp.ErrInvalidSignature,
		},
		{
			name:    "wrong_secret_rejected",
			rawSig:  signBody(t, "wrong_secret", freshTSFixed, body),
			rawTS:   freshTSFixed,
			body:    body,
			wantErr: slackhttp.ErrInvalidSignature,
		},
		{
			name:    "garbage_signature_rejected",
			rawSig:  "v0=notahexstring",
			rawTS:   freshTSFixed,
			body:    body,
			wantErr: slackhttp.ErrInvalidSignature,
		},
		{
			name:    "malformed_timestamp_rejected",
			rawSig:  validSig,
			rawTS:   "not-a-number",
			body:    body,
			wantErr: slackhttp.ErrStaleTimestamp,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Build a Verifier with a fixed clock so the replay-window check is stable.
			v := slackhttp.NewVerifier(testSecret)
			v.SetNow(func() time.Time { return nowFixed }) // exposed via a test-only setter

			err := v.Verify(tc.rawSig, tc.rawTS, tc.body)

			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Verify: got unexpected error %v", err)
				}

				return
			}

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Verify: got %v, want %v", err, tc.wantErr)
			}
		})
	}
}
