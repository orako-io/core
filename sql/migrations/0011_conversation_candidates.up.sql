-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Pool dispatch: the candidates computed when a question targets domains
-- instead of one member. excluded_at marks candidates released for silence
-- (they do not see the conversation again).
CREATE TABLE conversation_candidates (
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    member_id       uuid NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    invited_at      timestamptz NOT NULL DEFAULT now(),
    excluded_at     timestamptz,
    PRIMARY KEY (conversation_id, member_id)
);

CREATE INDEX idx_conversation_candidates_member ON conversation_candidates (member_id);
