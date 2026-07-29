-- SPDX-License-Identifier: AGPL-3.0-or-later

-- A 'reserving' row is a delivery that never confirmed 'posted' (its Deliver
-- outcome, or the finalize write, was lost) — reassign it to 'failed' before
-- dropping 'reserving' from the pre-0018 CHECK, mirroring how 0013's down
-- reassigns discord-bound members to 'dashboard' before re-adding its
-- stricter constraint. Without this, the down migration fails whenever a
-- 'reserving' row exists.
UPDATE provider_messages SET state = 'failed' WHERE state = 'reserving';

ALTER TABLE provider_messages DROP CONSTRAINT provider_messages_state_check;
ALTER TABLE provider_messages
    ADD CONSTRAINT provider_messages_state_check
        CHECK (state IN ('posted', 'claimed_won', 'claimed_lost', 'released', 'resolved', 'failed'));
