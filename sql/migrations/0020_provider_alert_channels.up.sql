-- Per-provider alert channels become a list: a project's provider can post
-- escalation alerts to several channels instead of one. Replaces the single
-- project_providers.alert_channel_id (added in 0013) with a TEXT[] column.
-- The org-wide default_alert_channel_id (organizations) stays as the fallback.

ALTER TABLE project_providers
    ADD COLUMN alert_channel_ids TEXT[] NOT NULL DEFAULT '{}';

-- Carry any existing single channel into the new list.
UPDATE project_providers
SET alert_channel_ids = ARRAY[alert_channel_id]
WHERE alert_channel_id <> '';

ALTER TABLE project_providers
    DROP COLUMN alert_channel_id;
