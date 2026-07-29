// SPDX-License-Identifier: AGPL-3.0-or-later

package model_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/orako-io/core/internal/application/domain/model"
)

// TestNormalizeTags proves the facet canonicalization: trim, lowercase, drop
// empties, dedup preserving order, and cap count and per-label length.
func TestNormalizeTags(t *testing.T) {
	t.Parallel()

	t.Run("trims lowercases drops empties and dedups preserving order", func(t *testing.T) {
		t.Parallel()

		got := model.NormalizeTags([]string{"  Postgres ", "postgres", "", "  ", "Auth", "auth"})
		want := []string{"postgres", "auth"}

		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("nil input yields non-nil empty slice", func(t *testing.T) {
		t.Parallel()

		got := model.NormalizeTags(nil)
		if got == nil {
			t.Fatal("want non-nil empty slice, got nil")
		}

		if len(got) != 0 {
			t.Fatalf("want empty slice, got %v", got)
		}
	})

	t.Run("caps count at MaxTagCount", func(t *testing.T) {
		t.Parallel()

		in := make([]string, model.MaxTagCount+5)
		for i := range in {
			in[i] = "tag" + strings.Repeat("x", i)
		}

		if got := model.NormalizeTags(in); len(got) != model.MaxTagCount {
			t.Fatalf("want %d tags, got %d", model.MaxTagCount, len(got))
		}
	})

	t.Run("caps label length at MaxTagRunes", func(t *testing.T) {
		t.Parallel()

		got := model.NormalizeTags([]string{strings.Repeat("z", model.MaxTagRunes+10)})
		if len(got) != 1 {
			t.Fatalf("want 1 tag, got %v", got)
		}

		if runes := len([]rune(got[0])); runes != model.MaxTagRunes {
			t.Fatalf("want label capped to %d runes, got %d", model.MaxTagRunes, runes)
		}
	})
}

// TestNormalizeSummary proves the summary is trimmed and capped at MaxSummaryRunes.
func TestNormalizeSummary(t *testing.T) {
	t.Parallel()

	t.Run("trims surrounding whitespace", func(t *testing.T) {
		t.Parallel()

		if got := model.NormalizeSummary("  a gist  "); got != "a gist" {
			t.Fatalf("want %q, got %q", "a gist", got)
		}
	})

	t.Run("caps at MaxSummaryRunes", func(t *testing.T) {
		t.Parallel()

		got := model.NormalizeSummary(strings.Repeat("s", model.MaxSummaryRunes+50))
		if runes := len([]rune(got)); runes != model.MaxSummaryRunes {
			t.Fatalf("want %d runes, got %d", model.MaxSummaryRunes, runes)
		}
	})
}
