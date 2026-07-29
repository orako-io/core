-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0045_conversation_history_search: the deterministic, embedding-free history
-- search engine (P3). History = the conversations themselves (ALL statuses),
-- searched with Postgres FTS + pg_trgm. Both ship with stock Postgres (pg_trgm
-- is a contrib module, unlike pgvector/ONNX), so the install no longer needs a
-- non-stock extension or a native model runtime — that is the whole point.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- fts: a STORED tsvector over the curated + raw text of a conversation. The
-- 'simple' config (no stemming) matches exact tokens across FR+EN — identifiers,
-- versions, acronyms — which is exactly what a dense embedding conflates.
-- tags/entities are folded in as single lexemes via array_to_tsvector (each
-- label becomes one lexeme). array_to_tsvector and to_tsvector(regconfig, text)
-- are IMMUTABLE, so they are allowed in a generated column; array_to_string is
-- only STABLE and would be rejected here (see search_labels below for the fuzzy
-- text arm that does need it).
ALTER TABLE conversations
    ADD COLUMN fts tsvector
    GENERATED ALWAYS AS (
        to_tsvector('simple',
            coalesce(summary, '') || ' ' ||
            coalesce(question, '') || ' ' ||
            coalesce(title, ''))
        || array_to_tsvector(tags)
        || array_to_tsvector(entities)
    ) STORED;

CREATE INDEX idx_conversations_fts ON conversations USING gin (fts);

-- search_labels: a flat text join of tags + entities for pg_trgm fuzzy matching
-- (typo/synonym tolerance on facet terms, so a query naming an existing
-- tag/entity still hits even without a lexical FTS match). array_to_string is
-- only STABLE and is rejected inside a generated column ("generation expression
-- is not immutable"), so it is wrapped in an IMMUTABLE SQL function — its body
-- is deterministic for text[] input, which the STABLE marking is merely
-- conservative about.
CREATE FUNCTION conversation_search_labels(tags text[], entities text[])
    RETURNS text
    LANGUAGE sql
    IMMUTABLE
    PARALLEL SAFE
    AS $$
        SELECT array_to_string(tags, ' ') || ' ' || array_to_string(entities, ' ')
    $$;

ALTER TABLE conversations
    ADD COLUMN search_labels text
    GENERATED ALWAYS AS (conversation_search_labels(tags, entities)) STORED;

CREATE INDEX idx_conversations_labels_trgm
    ON conversations USING gin (search_labels gin_trgm_ops);
