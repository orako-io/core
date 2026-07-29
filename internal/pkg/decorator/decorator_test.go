// SPDX-License-Identifier: AGPL-3.0-or-later

package decorator

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

type fakeHandler struct {
	out int
	err error
}

func (f fakeHandler) Handle(_ context.Context, _ int) (int, error) { return f.out, f.err }

type captureMetrics struct {
	name  string
	err   error
	calls int
}

func (c *captureMetrics) ObserveHandler(name string, _ time.Duration, err error) {
	c.name = name
	c.err = err
	c.calls++
}

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// TestApply proves the result-handler decorator observes the execution and
// passes the result/error through untouched, on both the ok and error paths.
func TestApply(t *testing.T) {
	t.Parallel()

	m := &captureMetrics{}
	h := Apply[int, int]("Query", fakeHandler{out: 42}, discard(), m)

	out, err := h.Handle(context.Background(), 1)
	if out != 42 || err != nil {
		t.Fatalf("passthrough: out=%d err=%v, want 42/nil", out, err)
	}

	if m.calls != 1 || m.name != "Query" || m.err != nil {
		t.Fatalf("metrics(ok): %+v", m)
	}

	boom := errors.New("boom")
	m2 := &captureMetrics{}
	h2 := Apply[int, int]("Query", fakeHandler{err: boom}, discard(), m2)

	if _, err = h2.Handle(context.Background(), 1); !errors.Is(err, boom) {
		t.Fatalf("error passthrough: got %v", err)
	}

	if m2.calls != 1 || m2.err == nil {
		t.Fatalf("metrics(err): %+v", m2)
	}
}
