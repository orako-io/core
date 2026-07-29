// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"strings"
	"unicode/utf8"
)

// Caps on the curated history-metadata fields. They bound the facet arrays and
// the summary so a single conversation cannot blow up the search index or the
// tag vocabulary, and are the single source of truth for both the write-side
// normalization here and the MCP tool descriptions that surface them to agents.
const (
	// MaxSummaryRunes caps the agent-curated one-line gist.
	MaxSummaryRunes = 500
	// MaxTagCount caps how many tags/entities a conversation may carry after dedup.
	MaxTagCount = 20
	// MaxTagRunes caps the length of a single tag/entity label.
	MaxTagRunes = 60
)

// NormalizeSummary trims surrounding whitespace and caps the summary at
// MaxSummaryRunes characters (counted in Unicode runes). It never generates
// text — it only trims what the agent wrote.
func NormalizeSummary(summary string) string {
	return truncateRunes(strings.TrimSpace(summary), MaxSummaryRunes)
}

// NormalizeTags canonicalizes a slice of tag/entity labels: each label is
// trimmed and lowercased, empties are dropped, duplicates are removed preserving
// first-seen order, every label is capped at MaxTagRunes, and the result is
// capped at MaxTagCount labels. It always returns a non-nil slice (empty when
// nothing survives) so callers can persist it into a NOT NULL TEXT[] column.
func NormalizeTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))

	for _, tag := range tags {
		label := truncateRunes(strings.ToLower(strings.TrimSpace(tag)), MaxTagRunes)
		if label == "" {
			continue
		}

		if _, dup := seen[label]; dup {
			continue
		}

		seen[label] = struct{}{}
		out = append(out, label)

		if len(out) == MaxTagCount {
			break
		}
	}

	return out
}

// truncateRunes returns s capped at maxRunes characters, cutting on a rune
// boundary; shorter strings are returned unchanged.
func truncateRunes(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}

	return string([]rune(s)[:maxRunes])
}
