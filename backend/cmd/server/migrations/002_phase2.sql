
ALTER TABLE backup_sources
  ADD COLUMN auth_type text NOT NULL DEFAULT 'token' CHECK (auth_type IN ('token','password')),
  ADD COLUMN secret_enc bytea,
  ADD COLUMN tls_verify boolean NOT NULL DEFAULT true,
  ADD COLUMN ca_pem text NOT NULL DEFAULT '',
  ADD COLUMN last_poll_at timestamptz,
  ADD COLUMN last_error text NOT NULL DEFAULT '',
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
CREATE UNIQUE INDEX backup_sources_name_uq ON backup_sources(lower(name));

CREATE TABLE backup_workloads (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  source_id uuid NOT NULL REFERENCES backup_sources(id) ON DELETE CASCADE,
  vmid int NOT NULL,
  name text NOT NULL DEFAULT '',
  node text NOT NULL DEFAULT '',
  kind text NOT NULL DEFAULT 'qemu',
  status text NOT NULL DEFAULT '',
  template boolean NOT NULL DEFAULT false,
  last_seen timestamptz NOT NULL DEFAULT now(),
  UNIQUE (source_id, vmid)
);
CREATE TABLE vzdump_tasks (
  source_id uuid NOT NULL REFERENCES backup_sources(id) ON DELETE CASCADE,
  upid text NOT NULL,
  node text NOT NULL,
  started_at timestamptz,
  ended_at timestamptz,
  status text NOT NULL DEFAULT '',
  PRIMARY KEY (source_id, upid)
);
CREATE TABLE backup_sessions (
  id bigserial PRIMARY KEY,
  source_id uuid NOT NULL REFERENCES backup_sources(id) ON DELETE CASCADE,
  workload_id uuid REFERENCES backup_workloads(id) ON DELETE SET NULL,
  vmid int NOT NULL,
  upid text NOT NULL,
  node text NOT NULL DEFAULT '',
  started_at timestamptz,
  ended_at timestamptz,
  status text NOT NULL CHECK (status IN ('success','warning','failed')),
  message text NOT NULL DEFAULT '',
  size_bytes bigint NOT NULL DEFAULT 0,
  UNIQUE (source_id, upid, vmid)
);
CREATE INDEX ON backup_sessions(source_id, vmid);
CREATE INDEX ON backup_sessions((coalesce(ended_at, started_at)) DESC);
CREATE TABLE backup_points (
  id bigserial PRIMARY KEY,
  source_id uuid NOT NULL REFERENCES backup_sources(id) ON DELETE CASCADE,
  storage text NOT NULL,
  node text NOT NULL DEFAULT '',
  volid text NOT NULL,
  vmid int NOT NULL,
  ctime timestamptz NOT NULL,
  size_bytes bigint NOT NULL DEFAULT 0,
  UNIQUE (source_id, volid)
);
CREATE INDEX ON backup_points(source_id, vmid);
CREATE TABLE source_poll_runs (
  id bigserial PRIMARY KEY,
  source_id uuid NOT NULL REFERENCES backup_sources(id) ON DELETE CASCADE,
  started_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  status text NOT NULL DEFAULT 'running',
  error text NOT NULL DEFAULT '',
  counts jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX ON source_poll_runs(source_id, started_at DESC);
