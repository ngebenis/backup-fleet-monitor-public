CREATE TABLE IF NOT EXISTS abb_tasks (
  source_id  uuid   NOT NULL REFERENCES backup_sources(id) ON DELETE CASCADE,
  task_id    bigint NOT NULL,
  task_name  text   NOT NULL DEFAULT '',
  raw        jsonb  NOT NULL DEFAULT '{}'::jsonb,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (source_id, task_id)
);
