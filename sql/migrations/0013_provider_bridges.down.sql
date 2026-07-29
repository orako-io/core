-- SPDX-License-Identifier: AGPL-3.0-or-later

ALTER TABLE project_providers
    DROP COLUMN alert_channel_id;

ALTER TABLE organizations
    DROP COLUMN alert_timeout_seconds,
    DROP COLUMN default_alert_channel_id;

-- Discord is only a valid delivery_channel value from 0013 onward; the
-- pre-0013 CHECK re-added below rejects it. Reassign any member currently
-- bound to it before re-adding the stricter constraint, or the down
-- migration fails whenever a discord-channel member row exists.
UPDATE members SET delivery_channel = 'dashboard' WHERE delivery_channel = 'discord';

ALTER TABLE members DROP CONSTRAINT members_delivery_channel_check;
ALTER TABLE members
    ADD CONSTRAINT members_delivery_channel_check
        CHECK (delivery_channel IN ('slack', 'teams', 'telegram', 'dashboard'));

ALTER TABLE members
    DROP COLUMN teams_user_id,
    DROP COLUMN discord_user_id,
    DROP COLUMN binding_error;

DROP TABLE provider_messages;
