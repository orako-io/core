-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Hub-and-spoke phase 2: the claim/release/second-opinion model is retired.
-- A conversation is a multi-participant discussion; specialist_member_id is
-- now a descriptive first-responder label (stamped by the first ANSWER, CAS).
--
-- Dropped here: the two org knobs whose rules no longer exist — the
-- silent-release rung (silence_timeout_seconds) and the second-opinion
-- capture floor (capture_second_opinions). Everything else stays:
-- conversation_candidates remains the contacted set, nudged_at/alerted_at
-- still back the surviving escalation rungs, and legacy message roles
-- ('second_opinion') / provider_message states ('claimed_won'/'claimed_lost'/
-- 'released') stay readable on historical rows.
ALTER TABLE organizations DROP COLUMN IF EXISTS silence_timeout_seconds;
ALTER TABLE organizations DROP COLUMN IF EXISTS capture_second_opinions;
