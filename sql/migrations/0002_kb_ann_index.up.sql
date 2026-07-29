-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0002_kb_ann_index: NEUTRALIZED (P5b). Originally created an ivfflat ANN index
-- on kb_entries.embedding (pgvector). That column no longer exists (removed in
-- 0001) and kb_entries is dropped in 0046, so this step is now a no-op to keep
-- the migration chain applying on stock Postgres with no pgvector extension.

SELECT 1;
