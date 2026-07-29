-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Down: drop the curated knowledge table and its dedicated label function. The
-- pg_trgm extension is left in place — it is shared with conversations (0045).

DROP TABLE IF EXISTS knowledge_entries;
DROP FUNCTION IF EXISTS knowledge_search_labels(text[], text[]);
