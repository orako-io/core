// SPDX-License-Identifier: AGPL-3.0-or-later

// Package decorator adds cross-cutting concerns (structured logging + metrics)
// to CQRS handlers via generic decorators, so no handler carries that plumbing
// itself (cohesion — see https://threedots.tech/post/increasing-cohesion-in-go-with-generic-decorators/).
//
// Apply wraps a result-returning handler with a metrics decorator (inner) then
// a logging decorator (outer). Metrics land on the swappable Metrics port, so the
// backend (no-op / slog / Prometheus / OTLP …) is chosen at the composition root
// without touching this package or any handler.
package decorator

import (
	"context"
	"log/slog"
	"time"
)

// Handler is a query or result-returning command handler.
type Handler[In, Out any] interface {
	Handle(ctx context.Context, in In) (Out, error)
}

// Metrics is the swappable observability port. Implementations live under
// internal/pkg/metrics and must be cheap and non-blocking. A nil Metrics
// is tolerated (metrics are simply skipped).
type Metrics interface {
	// ObserveHandler records one handler execution: its name, wall-clock
	// duration, and whether it errored.
	ObserveHandler(name string, dur time.Duration, err error)
}

// Apply wraps a result-returning handler with metrics + logging.
func Apply[In, Out any](name string, h Handler[In, Out], log *slog.Logger, m Metrics) Handler[In, Out] {
	return handlerDecorator[In, Out]{name: name, base: h, log: log, metrics: m}
}

type handlerDecorator[In, Out any] struct {
	name    string
	base    Handler[In, Out]
	log     *slog.Logger
	metrics Metrics
}

func (d handlerDecorator[In, Out]) Handle(ctx context.Context, in In) (Out, error) {
	start := time.Now()
	out, err := d.base.Handle(ctx, in)
	observe(ctx, d.name, start, err, d.log, d.metrics)

	return out, err
}

// observe emits the metric and a structured log line for one handler execution.
// Shared by both decorators so logging and metric semantics stay identical.
func observe(ctx context.Context, name string, start time.Time, err error, log *slog.Logger, m Metrics) {
	dur := time.Since(start)

	if m != nil {
		m.ObserveHandler(name, dur, err)
	}

	if err != nil {
		log.ErrorContext(ctx, "handler failed",
			slog.String("handler", name),
			slog.Int64("duration_ms", dur.Milliseconds()),
			slog.String("error", err.Error()))

		return
	}

	log.DebugContext(ctx, "handler ok",
		slog.String("handler", name),
		slog.Int64("duration_ms", dur.Milliseconds()))
}
