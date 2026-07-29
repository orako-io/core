-- SPDX-License-Identifier: AGPL-3.0-or-later

-- conversation_participants holds members EXPLICITLY added to an existing
-- conversation (Option A: add_participant) so they receive the fan-out of every
-- question and answer. Distinct from conversation_candidates (pool-claim
-- semantics): a participant is never released for silence and always receives
-- the thread. The effective participant set is the asker + the assigned
-- specialist + the rows here. added_by records who added them.
CREATE TABLE conversation_participants (
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    member_id       uuid NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    added_by        uuid REFERENCES members(id) ON DELETE SET NULL,
    added_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, member_id)
);

CREATE INDEX idx_conversation_participants_member ON conversation_participants (member_id);
