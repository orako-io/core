// SPDX-License-Identifier: AGPL-3.0-or-later

// Package metrics provides swappable adapters for the decorator.Metrics port.
// The backend is chosen at startup via ORAKO_METRICS_KIND — so the observability tool can be changed
// freely (on-prem, hosted, or plain stdout) without touching handlers.
//
// Adapters here:
//   - Noop — no metrics (default; zero cost, zero deps; the "classic" path).
//   - Slog — metrics emitted as structured log lines (metrics-as-logs → stdout
//     → whatever ships your logs: Loki, Axiom, Grafana Cloud, …).
//
// A Prometheus adapter (self-host, on-prem) or an OTLP/OpenTelemetry adapter
// (vendor-neutral) can be added as another implementation of the same port and
// selected here — no other code changes.
package metrics

import (
	"log/slog"
	"time"

	"github.com/orako-io/core/internal/pkg/decorator"
)

// NewFromEnv selects the metrics adapter from ORAKO_METRICS_KIND
// ("noop" | "slog"; anything else → noop). Never returns nil.
func NewFromEnv(kind string, log *slog.Logger) decorator.Metrics {
	switch kind {
	case "slog":
		log.Info("metrics: slog adapter (metrics emitted as structured logs)")

		return Slog{log: log}
	default:
		return Noop{}
	}
}

// Noop discards all metrics. It is the default and adds no runtime cost.
type Noop struct{}

// ObserveHandler does nothing.
func (Noop) ObserveHandler(_ string, _ time.Duration, _ error) {}

// Slog emits each handler observation as a structured log record on the shared
// logger, so any log pipeline doubles as a cheap metrics sink.
type Slog struct {
	log *slog.Logger
}

// ObserveHandler logs the handler name, duration, and success flag.
func (s Slog) ObserveHandler(name string, dur time.Duration, err error) {
	s.log.Info("handler.metric",
		slog.String("handler", name),
		slog.Int64("duration_ms", dur.Milliseconds()),
		slog.Bool("success", err == nil))
}
