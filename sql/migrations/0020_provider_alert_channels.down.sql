-- Revert per-provider alert channels back to a single column, keeping the
-- first channel of any list.

ALTER TABLE project_providers
    ADD COLUMN alert_channel_id TEXT NOT NULL DEFAULT '';

UPDATE project_providers
SET alert_channel_id = alert_channel_ids[1]
WHERE cardinality(alert_channel_ids) > 0;

ALTER TABLE project_providers
    DROP COLUMN alert_channel_ids;
