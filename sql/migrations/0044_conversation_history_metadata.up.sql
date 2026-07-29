-- SPDX-License-Identifier: AGPL-3.0-or-later
-- 0044: living history metadata on conversations. summary is a curated one-line
-- gist, tags/entities are normalized facet arrays. All agent-written at
-- write-time; the search side (later phases) reads them. Additive with safe
-- defaults so existing rows stay valid.
ALTER TABLE conversations ADD COLUMN summary TEXT NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN tags TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE conversations ADD COLUMN entities TEXT[] NOT NULL DEFAULT '{}';
