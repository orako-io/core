-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0025_kb_entry_aliases: the query-alias flywheel. Real search queries that land
-- on an entry with positive feedback are appended to alias_text, which is folded
-- into the fts column — so future searches in users' own wording match via the
-- lexical arm, with zero LLM. alias_text is plain text (not an array) so its
-- phrases are tokenised by to_tsvector rather than stored as single lexemes.

ALTER TABLE kb_entries ADD COLUMN alias_text text NOT NULL DEFAULT '';

-- Redefine the fts generated column to include alias_text. A generated column's
-- expression cannot be altered in place, so drop and recreate (0024 form + aliases).
DROP INDEX IF EXISTS idx_kb_entries_fts;
ALTER TABLE kb_entries DROP COLUMN fts;
ALTER TABLE kb_entries
    ADD COLUMN fts tsvector
    GENERATED ALWAYS AS (
        to_tsvector('simple', question || ' ' || coalesce(answer, '') || ' ' || alias_text)
        || array_to_tsvector(tags)
    ) STORED;
CREATE INDEX idx_kb_entries_fts ON kb_entries USING gin (fts);
