-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0017_kb_contested: closure/KB divergence layer (phase 3 of second-opinion).
-- A KB entry created from a conversation with an unresolved second opinion
-- (model.MessageRoleSecondOpinion, 0016_second_opinion) is flagged contested
-- so a later SearchKnowledge hit tells the agent to re-ask for confirmation
-- instead of trusting a full-confidence answer. Existing rows default false
-- (no prior entry was distilled from a candidate divergence this schema
-- didn't track yet).

ALTER TABLE kb_entries
    ADD COLUMN contested BOOLEAN NOT NULL DEFAULT false;
