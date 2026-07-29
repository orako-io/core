-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0046_drop_kb_entries (down): NOT reversible. This is a forward-only refactor
-- (P5b) that retires the knowledge-base table and pgvector for good — history
-- search is FTS + pg_trgm on conversations now. Recreating kb_entries would
-- require the pgvector extension we deliberately removed, so we do not restore
-- it. No-op.

SELECT 1;
