DROP INDEX IF EXISTS idx_event_log_global_seq;
ALTER TABLE event_log DROP COLUMN IF EXISTS global_seq;
