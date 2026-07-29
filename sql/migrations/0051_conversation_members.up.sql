-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0051_conversation_members: collapse conversation_candidates (pool dispatch)
-- and conversation_participants (explicitly added) into ONE membership table.
-- With hub-and-spoke threads there is a single membership per (conversation,
-- member), distinguished by HOW they joined — dispatched (invited_at) vs
-- explicitly added (added_at) — not two tables. A member can be both: a row
-- carries both column sets.
--
--   active candidate     = invited_at IS NOT NULL AND excluded_at IS NULL
--   explicit participant = added_at   IS NOT NULL

CREATE TABLE conversation_members (
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    member_id       uuid NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    invited_at      timestamptz,                                     -- ex-candidate: in the dispatch pool; NULL = never dispatched
    excluded_at     timestamptz,                                     -- removed from the pool
    added_at        timestamptz,                                     -- ex-participant: explicitly added; NULL = never explicitly added
    added_by        uuid REFERENCES members(id) ON DELETE SET NULL,  -- who added them (NULL = auto fan-out)
    PRIMARY KEY (conversation_id, member_id)
);

CREATE INDEX idx_conversation_members_member   ON conversation_members (member_id);
CREATE INDEX idx_conversation_members_added_by ON conversation_members (added_by) WHERE added_by IS NOT NULL;

-- Merge existing rows. Candidates first (they carry invited_at/excluded_at),
-- then upsert participants onto the same (conversation_id, member_id) key so a
-- member present in BOTH tables ends up as one row with both column sets set.
INSERT INTO conversation_members (conversation_id, member_id, invited_at, excluded_at)
SELECT conversation_id, member_id, invited_at, excluded_at
FROM conversation_candidates;

INSERT INTO conversation_members (conversation_id, member_id, added_at, added_by)
SELECT conversation_id, member_id, added_at, added_by
FROM conversation_participants
ON CONFLICT (conversation_id, member_id)
DO UPDATE SET added_at = EXCLUDED.added_at,
              added_by = EXCLUDED.added_by;

DROP TABLE conversation_candidates;
DROP TABLE conversation_participants;
