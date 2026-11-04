-- Phase 3: Synology Active Backup for Business + Veeam EM side tables.
ALTER TABLE backup_workloads ADD COLUMN IF NOT EXISTS ext_id text NOT NULL DEFAULT '';
ALTER TABLE backup_sessions  ADD COLUMN IF NOT EXISTS raw_status text NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS abb_events (
  id bigserial PRIMARY KEY,
  source_id uuid NOT NULL REFERENCES backup_sources(id) ON DELETE CASCADE,
  log_id bigint NOT NULL,
  log_time timestamptz NOT NULL,
  log_level int NOT NULL DEFAULT 0,
  log_type int NOT NULL DEFAULT 0,
  error_code int NOT NULL DEFAULT 0,
  device_id int NOT NULL DEFAULT 0,
  device_name text NOT NULL DEFAULT '',
  task_id int NOT NULL DEFAULT 0,
  task_name text NOT NULL DEFAULT '',
  user_name text NOT NULL DEFAULT '',
  severity text NOT NULL DEFAULT 'info',
  category text NOT NULL DEFAULT '',
  UNIQUE (source_id, log_id)
);
CREATE INDEX IF NOT EXISTS abb_events_time ON abb_events(source_id, log_time DESC);
