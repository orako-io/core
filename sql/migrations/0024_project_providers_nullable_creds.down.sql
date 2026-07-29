-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Restore NOT NULL. Rows with NULL credentials must be backfilled/removed first
-- for this to succeed.

ALTER TABLE project_providers ALTER COLUMN credentials SET NOT NULL;
