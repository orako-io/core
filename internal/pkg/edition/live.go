// SPDX-License-Identifier: AGPL-3.0-or-later

package edition

import "sync/atomic"

// Live is a concurrency-safe holder for the resolved edition that can be swapped
// at runtime. The edition is resolved once at boot, but a self-host license
// expires (or renews) between restarts — a background refresh re-resolves it and
// Stores the result here, and the resource gates read Current() on every check.
// So "stop paying → key expires → caps re-apply" takes effect without a restart.
type Live struct {
	ptr atomic.Pointer[Edition]
}

// NewLive seeds a holder with the boot-time edition.
func NewLive(e Edition) *Live {
	var l Live
	l.Store(e)

	return &l
}

// Current returns the latest edition. Falls back to Community if never stored
// (defensive; NewLive always seeds it).
func (l *Live) Current() Edition {
	if p := l.ptr.Load(); p != nil {
		return *p
	}

	return Edition{Kind: Community, Limits: CommunityLimits, Features: nil}
}

// Store swaps in a new edition (copied, so callers can't mutate it afterwards).
func (l *Live) Store(e Edition) {
	c := e
	l.ptr.Store(&c)
}
