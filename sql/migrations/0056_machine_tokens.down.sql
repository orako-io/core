-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Down: drop the reserved machine-token pseudo-client (ON DELETE CASCADE
-- takes every machine token row with it — expected for a schema rollback)
-- and the label column.

DELETE FROM oauth_clients WHERE client_id = 'mcp_client_machine_tokens';
ALTER TABLE oauth_tokens DROP COLUMN label;
