-- Phase 3: keep backup KPI clean. Non-backup ABB runs (restore/verify) live in their own table.
CREATE TABLE IF NOT EXISTS abb_results (
  id bigserial PRIMARY KEY,
  source_id uuid NOT NULL REFERENCES backup_sources(id) ON DELETE CASCADE,
  result_id bigint NOT NULL,
  task_id int NOT NULL DEFAULT 0,
  task_name text NOT NULL DEFAULT '',
  job_action int NOT NULL DEFAULT 0,
  status int NOT NULL DEFAULT 0,
  error_count int NOT NULL DEFAULT 0,
  warning_count int NOT NULL DEFAULT 0,
  started_at timestamptz,
  ended_at timestamptz,
  raw jsonb NOT NULL DEFAULT '{}'::jsonb,
  UNIQUE (source_id, result_id)
);
CREATE INDEX IF NOT EXISTS abb_results_time ON abb_results(source_id, ended_at DESC);
