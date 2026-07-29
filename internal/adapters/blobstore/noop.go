// SPDX-License-Identifier: AGPL-3.0-or-later

package blobstore

import (
	"context"
	"io"
	"time"
)

// Noop is the disabled blob store: every operation fails with
// ErrDisabled and Enabled() is false. Wired when the S3 env is
// absent, so attachment features degrade off with a clear signal rather than a
// nil-pointer panic.
type Noop struct{}

// Put returns ErrDisabled.
func (Noop) Put(context.Context, string, io.Reader, string, int64) error {
	return ErrDisabled
}

// Get returns ErrDisabled.
func (Noop) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, ErrDisabled
}

// SignedGetURL returns ErrDisabled.
func (Noop) SignedGetURL(context.Context, string, time.Duration) (string, error) {
	return "", ErrDisabled
}

// Delete is a no-op when storage is disabled.
func (Noop) Delete(context.Context, string) error { return nil }

// Enabled reports false.
func (Noop) Enabled() bool { return false }
