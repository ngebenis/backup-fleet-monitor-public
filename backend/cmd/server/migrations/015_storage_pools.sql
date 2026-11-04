CREATE TABLE IF NOT EXISTS storage_pools(
  id bigserial PRIMARY KEY,
  source_id uuid NOT NULL REFERENCES backup_sources(id) ON DELETE CASCADE,
  node text NOT NULL DEFAULT '',
  name text NOT NULL,
  kind text NOT NULL DEFAULT '',
  content text NOT NULL DEFAULT '',
  shared boolean NOT NULL DEFAULT false,
  total_bytes bigint NOT NULL DEFAULT 0,
  used_bytes bigint NOT NULL DEFAULT 0,
  online boolean NOT NULL DEFAULT true,
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(source_id,node,name)
);
