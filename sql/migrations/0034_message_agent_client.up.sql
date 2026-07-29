-- SPDX-License-Identifier: AGPL-3.0-or-later

-- agent_client records WHICH coding agent authored a message, captured from the
-- MCP client's declared name (e.g. "claude-code", "codex-cli"). Only meaningful
-- for source='agent' messages; empty for human/system. Lets the dashboard show
-- "Name · Claude Code" with the agent's logo. Empty when unknown (the client did
-- not declare an identity).
ALTER TABLE messages ADD COLUMN agent_client text NOT NULL DEFAULT '';
