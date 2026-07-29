-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0018_ledger_reserve: closes the double-delivery retry window between a
-- successful Deliver and the ledger write that follows it. Previously:
-- Deliver(...) succeeds, then the ledger write (Upsert in the pool fan-out /
-- SetState in the bridge projector) fails, the watermill router retries the
-- whole message, and — because no ledger row existed yet for the pool fan-out
-- case (or the row's state guard hadn't advanced yet for the projector case)
-- — the retry calls Deliver AGAIN, sending a duplicate DM.
--
-- 'reserving' is the new lifecycle state: a row is now written (pool
-- fan-out) or transitioned into (projector re-delivery) as 'reserving'
-- BEFORE Deliver is ever called, then finalized to its real target state
-- afterward. A row already present for a candidate — in ANY state, including
-- 'reserving' — is what the existing notified-set / per-row state guards
-- already treat as "already handled", so a retry that finds a 'reserving'
-- row never re-enters Deliver for it. See
-- internal/application/delivery_notifier.go and bridge_projector.go for the
-- full write-up, including the accepted crash-window tradeoff (a row stuck
-- in 'reserving' after a lost Deliver outcome or a lost finalize is never
-- retried — favoring "no duplicate" over "no miss").

ALTER TABLE provider_messages DROP CONSTRAINT provider_messages_state_check;
ALTER TABLE provider_messages
    ADD CONSTRAINT provider_messages_state_check
        CHECK (state IN ('reserving', 'posted', 'claimed_won', 'claimed_lost', 'released', 'resolved', 'failed'));
