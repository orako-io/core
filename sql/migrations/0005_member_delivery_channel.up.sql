-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- 0005_member_delivery_channel: each member chooses where Orako reaches them.
--
-- members.delivery_channel — the member's preferred contact channel. Routing in
--   AskSpecialist resolves the specialist's channel to the matching provider.
--   'dashboard' is the default: it needs no external binding and always works,
--   so a member with no connector binding is never orphaned.
--
-- Channels other than 'dashboard' require the matching binding to be set
-- (slack_user_id / telegram_chat_id); that invariant is enforced in the
-- application layer, not the schema, because the binding may be filled in later.

ALTER TABLE members
    ADD COLUMN delivery_channel TEXT NOT NULL DEFAULT 'dashboard'
        CHECK (delivery_channel IN ('slack', 'teams', 'telegram', 'dashboard'));
