-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0025_kb_entry_aliases (down): restore the 0024 fts column (question + answer +
-- tags, without aliases) and drop alias_text.

DROP INDEX IF EXISTS idx_kb_entries_fts;
ALTER TABLE kb_entries DROP COLUMN fts;
ALTER TABLE kb_entries
    ADD COLUMN fts tsvector
    GENERATED ALWAYS AS (
        to_tsvector('simple', question || ' ' || coalesce(answer, ''))
        || array_to_tsvector(tags)
    ) STORED;
CREATE INDEX idx_kb_entries_fts ON kb_entries USING gin (fts);

ALTER TABLE kb_entries DROP COLUMN alias_text;
