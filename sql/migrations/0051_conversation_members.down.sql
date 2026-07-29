-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0051_conversation_members (down): split the merged table back into the two
-- pre-merge tables and copy the rows back. conversation_candidates gets rows
-- with invited_at; conversation_participants gets rows with added_at. Recreates
-- both tables exactly as they stood before this migration (0011 + 0033 defs,
-- plus the idx_conversation_participants_added_by index added in 0050).

CREATE TABLE conversation_candidates (
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    member_id       uuid NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    invited_at      timestamptz NOT NULL DEFAULT now(),
    excluded_at     timestamptz,
    PRIMARY KEY (conversation_id, member_id)
);
CREATE INDEX idx_conversation_candidates_member ON conversation_candidates (member_id);

CREATE TABLE conversation_participants (
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    member_id       uuid NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    added_by        uuid REFERENCES members(id) ON DELETE SET NULL,
    added_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, member_id)
);
CREATE INDEX idx_conversation_participants_member   ON conversation_participants (member_id);
CREATE INDEX idx_conversation_participants_added_by ON conversation_participants (added_by);

INSERT INTO conversation_candidates (conversation_id, member_id, invited_at, excluded_at)
SELECT conversation_id, member_id, invited_at, excluded_at
FROM conversation_members
WHERE invited_at IS NOT NULL;

INSERT INTO conversation_participants (conversation_id, member_id, added_by, added_at)
SELECT conversation_id, member_id, added_by, added_at
FROM conversation_members
WHERE added_at IS NOT NULL;

DROP TABLE conversation_members;
