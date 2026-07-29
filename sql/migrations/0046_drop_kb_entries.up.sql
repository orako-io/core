-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0046_drop_kb_entries (P5b): drop the dead knowledge-base table and the
-- pgvector extension. After the ONNX/pgvector removal (P5a) no Go code reads or
-- writes kb_entries, and history search runs on conversations via FTS + pg_trgm
-- (0045). This removes the last pgvector dependency from the schema so fresh
-- installs need only stock Postgres.
--
-- Idempotent on both paths:
--   * Existing prod DB: kb_entries (with its embedding column + ANN indexes) and
--     the vector extension still exist from the original 0001/0002/0025 — both
--     are dropped here. CASCADE also removes kb_feedback (FK) and any leftover
--     index.
--   * Fresh stock-Postgres install: 0001 now creates kb_entries WITHOUT the
--     embedding column and never creates the vector extension, so the DROPs are
--     harmless no-ops (IF EXISTS).

DROP TABLE IF EXISTS kb_entries CASCADE;

DROP EXTENSION IF EXISTS vector;
