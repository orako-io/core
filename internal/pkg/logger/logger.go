// SPDX-License-Identifier: Apache-2.0

// Package logger builds the structured [log/slog] logger used across Orako.
package logger

import (
	"io"
	"log/slog"
	"strings"

	slogbetterstack "github.com/samber/slog-betterstack"
	slogmulti "github.com/samber/slog-multi"
)

// BetterStack configures the optional hosted Better Stack (Logtail) log sink.
// A zero Token disables it, leaving stdout-only — the classic path. This is the
// swappable seam: set/unset the token (via ORAKO_BETTERSTACK_SOURCE_TOKEN) to
// plug or unplug the vendor, no code change. The app never depends on it.
type BetterStack struct {
	// Token is the Better Stack source ingestion token. Empty = sink disabled.
	Token string `exhaustruct:"optional"`
	// Endpoint is the source's ingesting host (e.g. https://sXXXX.<region>.betterstackdata.com).
	// Empty falls back to the library default.
	Endpoint string `exhaustruct:"optional"`
}

// NewWithBetterStack builds the stdout JSON logger and, when bs.Token is set,
// fans every record out to Better Stack as well (structured logs — and, with
// ORAKO_METRICS_KIND=slog, the decorator metrics ride the same pipe). An empty
// token keeps the logger stdout-only.
func NewWithBetterStack(w io.Writer, level string, bs BetterStack) *slog.Logger {
	lv := parseLevel(level)

	stdout := slog.NewJSONHandler(w, &slog.HandlerOptions{
		AddSource:   false,
		Level:       lv,
		ReplaceAttr: nil,
	})

	if bs.Token == "" {
		return slog.New(stdout)
	}

	betterstack := slogbetterstack.Option{
		Level:    lv,
		Token:    bs.Token,
		Endpoint: bs.Endpoint,
	}.NewBetterstackHandler()

	return slog.New(slogmulti.Fanout(stdout, betterstack))
}

// parseLevel maps a textual level to its slog.Level, defaulting to info.
func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
