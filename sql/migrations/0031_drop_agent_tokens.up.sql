-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Personal agent tokens (orak_…) are retired: the orako CLI, their only
-- consumer, is deleted, and /mcp accepts only OAuth-issued mcp_at_ tokens.
DROP TABLE agent_tokens;
