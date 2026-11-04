-- Phase 3: Veeam Enterprise Manager jobs (read-only mirror).
CREATE TABLE IF NOT EXISTS veeam_jobs (
  id bigserial PRIMARY KEY,
  source_id uuid NOT NULL REFERENCES backup_sources(id) ON DELETE CASCADE,
  uid text NOT NULL,
  name text NOT NULL DEFAULT '',
  job_type text NOT NULL DEFAULT '',
  backup_server text NOT NULL DEFAULT '',
  platform text NOT NULL DEFAULT '',
  is_disabled boolean NOT NULL DEFAULT false,
  last_result text NOT NULL DEFAULT '',
  last_run timestamptz,
  raw jsonb NOT NULL DEFAULT '{}'::jsonb,
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (source_id, uid)
);
CREATE INDEX IF NOT EXISTS veeam_jobs_result ON veeam_jobs(source_id, last_result);
