-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Short human title for lists; agent-provided (server falls back to the
-- truncated question). Empty string = legacy rows.
ALTER TABLE conversations ADD COLUMN title text NOT NULL DEFAULT '';
