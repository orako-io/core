// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// History-metadata validation fields and guidance messages. The messages are
// written FOR the calling agent: a rejection tells it exactly what to curate so
// the conversation is findable later by the deterministic history search.
const (
	fieldSummary = "summary"
	fieldTags    = "tags"

	reasonSummaryRequired = "summary is required: one line describing what this conversation is about"
	reasonTagsRequired    = `at least one tag is required (lowercase topic/area labels, e.g. "billing", "discord")`
)

// normalizeRequiredMetadata normalizes the agent-curated history metadata and
// enforces the required-but-guided contract used by ask and resolve: a
// conversation must be born (and resolved) with a non-empty summary and at
// least one tag, so history search can find it. entities are normalized like
// tags but stay optional. It returns the canonical (normalized, non-nil)
// values, or a guidance-bearing errs.InvalidError naming exactly what to
// supply. Normalization lives here at the command (domain) boundary — never in
// a driving adapter.
func normalizeRequiredMetadata(summary string, tags, entities []string) (string, []string, []string, error) {
	normSummary := model.NormalizeSummary(summary)
	normTags := model.NormalizeTags(tags)
	normEntities := model.NormalizeTags(entities)

	if normSummary == "" {
		return "", nil, nil, errs.InvalidError{Field: fieldSummary, Reason: reasonSummaryRequired}
	}

	if len(normTags) == 0 {
		return "", nil, nil, errs.InvalidError{Field: fieldTags, Reason: reasonTagsRequired}
	}

	return normSummary, normTags, normEntities, nil
}

// resolveMetadata computes the FINAL history metadata for a resolve, letting a
// human resolving from the dashboard (Connect-RPC ResolveConversation, which
// carries no metadata fields) inherit the conversation's CURRENT curated
// summary/tags/entities — the ones set at ask/follow_up time. For each facet the
// agent-supplied value wins when present; otherwise it falls back to the
// existing curated value. The same required-but-guided contract as ask still
// holds on the RESULT: the final summary must be non-empty and there must be at
// least one tag — so a resolve where both supplied and existing are empty is
// still rejected, keeping the guarantee that resolved conversations always carry
// the metadata history search reads.
func resolveMetadata(
	summary string, tags, entities []string,
	curSummary string, curTags, curEntities []string,
) (string, []string, []string, error) {
	normSummary := model.NormalizeSummary(summary)
	if normSummary == "" {
		normSummary = model.NormalizeSummary(curSummary)
	}

	normTags := model.NormalizeTags(tags)
	if len(normTags) == 0 {
		normTags = model.NormalizeTags(curTags)
	}

	normEntities := model.NormalizeTags(entities)
	if len(normEntities) == 0 {
		normEntities = model.NormalizeTags(curEntities)
	}

	if normSummary == "" {
		return "", nil, nil, errs.InvalidError{Field: fieldSummary, Reason: reasonSummaryRequired}
	}

	if len(normTags) == 0 {
		return "", nil, nil, errs.InvalidError{Field: fieldTags, Reason: reasonTagsRequired}
	}

	return normSummary, normTags, normEntities, nil
}
