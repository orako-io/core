-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0024_kb_fts (down): drop the full-text index and generated column.

DROP INDEX IF EXISTS idx_kb_entries_fts;

ALTER TABLE kb_entries DROP COLUMN IF EXISTS fts;
