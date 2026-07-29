-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0024_kb_fts: add a generated full-text column over question + answer + tags,
-- with a GIN index, to power the lexical arm of hybrid search. The 'simple'
-- config (no stemming) is deliberate: it matches exact tokens across languages
-- (FR + EN) — identifiers, ticket numbers, code symbols, acronyms — which is
-- exactly what the dense embedding conflates. Built-in tsvector needs no
-- non-stock extension (works on any Postgres), unlike pg_search/BM25.
--
-- to_tsvector(regconfig, text) with an explicit config and array_to_tsvector are
-- both IMMUTABLE, so they are allowed in a STORED generated column. (Note:
-- array_to_string is only STABLE and would be rejected here — hence tags are
-- folded in via array_to_tsvector, each tag becoming one lexeme.)

ALTER TABLE kb_entries
    ADD COLUMN fts tsvector
    GENERATED ALWAYS AS (
        to_tsvector('simple', question || ' ' || coalesce(answer, ''))
        || array_to_tsvector(tags)
    ) STORED;

CREATE INDEX idx_kb_entries_fts ON kb_entries USING gin (fts);
