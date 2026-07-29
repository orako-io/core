-- SPDX-License-Identifier: AGPL-3.0-or-later
DROP INDEX IF EXISTS idx_conversations_labels_trgm;
DROP INDEX IF EXISTS idx_conversations_fts;
ALTER TABLE conversations DROP COLUMN IF EXISTS search_labels;
ALTER TABLE conversations DROP COLUMN IF EXISTS fts;
DROP FUNCTION IF EXISTS conversation_search_labels(text[], text[]);
-- pg_trgm is introduced solely by this migration; nothing else depends on it,
-- so dropping it on rollback is safe (guarded with IF EXISTS, and only after the
-- trgm index that referenced gin_trgm_ops is already gone above).
DROP EXTENSION IF EXISTS pg_trgm;
