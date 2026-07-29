-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0025_kb_hnsw_index: NEUTRALIZED (P5b). Originally replaced the ivfflat index
-- with an HNSW index on kb_entries.embedding (pgvector). That column no longer
-- exists (removed in 0001) and kb_entries is dropped in 0046, so this step is
-- now a no-op to keep the chain applying on stock Postgres (no pgvector).

SELECT 1;
