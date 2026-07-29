// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/errs"
)

// defaultVocabularyLimit caps how many distinct tags/entities the reinjected
// project vocabulary carries when the caller does not specify a limit. It keeps
// the reinjected list small enough to stay useful as a reuse hint.
const defaultVocabularyLimit = 50

// Vocabulary is the project's existing history-metadata vocabulary — the
// distinct tags and entities already curated on its conversations, most-used
// first. It is reinjected into the ask/resolve/follow_up tool responses so the
// agent reuses established labels instead of inventing synonyms (anti-sprawl).
type Vocabulary struct {
	Tags     []string
	Entities []string
}

// VocabularyQuery is the input for reading a project scope's existing history
// vocabulary.
type VocabularyQuery struct {
	// ProjectIDs scopes the vocabulary to exactly these projects (already
	// intersected with caller visibility by the transport). Empty yields an
	// empty vocabulary.
	ProjectIDs []uuid.UUID `exhaustruct:"optional"`
	// Limit caps the number of tags and entities returned (each), defaulting to
	// defaultVocabularyLimit when <= 0.
	Limit int `exhaustruct:"optional"`
}

// VocabularyReader is the read-side port for the project's existing history
// vocabulary. Satisfied by *conversation.Store.
type VocabularyReader interface {
	// HistoryVocabulary returns the distinct tags and entities curated on the
	// conversations of projectIDs, most-frequent first, each list capped at
	// limit. Archived projects are excluded; an empty projectIDs yields an empty
	// vocabulary.
	HistoryVocabulary(ctx context.Context, projectIDs []uuid.UUID, limit int) (Vocabulary, error)
}

// HistoryVocabularyHandler handles VocabularyQuery.
type HistoryVocabularyHandler struct {
	reader VocabularyReader
}

// MustNewHistoryVocabularyHandler builds a handler. It panics on a nil
// dependency.
func MustNewHistoryVocabularyHandler(reader VocabularyReader) HistoryVocabularyHandler {
	if reader == nil {
		panic("HistoryVocabularyHandler requires a non-nil VocabularyReader")
	}

	return HistoryVocabularyHandler{reader: reader}
}

// Handle returns the existing tag/entity vocabulary for the query's project
// scope, applying the default limit when unset.
func (h HistoryVocabularyHandler) Handle(ctx context.Context, q VocabularyQuery) (Vocabulary, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultVocabularyLimit
	}

	vocab, err := h.reader.HistoryVocabulary(ctx, q.ProjectIDs, limit)
	if err != nil {
		return Vocabulary{}, errs.InternalError{Err: fmt.Errorf("reading history vocabulary: %w", err)}
	}

	return vocab, nil
}
