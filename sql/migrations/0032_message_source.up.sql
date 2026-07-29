-- SPDX-License-Identifier: AGPL-3.0-or-later

-- Records how a message was authored: 'human' (typed in the dashboard or a chat
-- provider), 'agent' (posted through the MCP tools by a coding agent on the
-- member's behalf), or 'system' (routing/claim/timeout notes). Lets the UI mark
-- an agent-authored message as "Name (via agent)" so a reader knows it was not
-- typed by the person directly. Existing rows default to 'human' (the
-- conservative attribution — never falsely claim agent authorship).
ALTER TABLE messages ADD COLUMN source text NOT NULL DEFAULT 'human';
