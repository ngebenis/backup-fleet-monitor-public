-- Phase 3f: Veeam Enterprise Manager SQL collector (push ingest).
-- The EM REST API is unusable on the affected Veeam EM 12.0 build (logon defect), so a read-only
-- collector on the Veeam host queries the reporting DB and pushes JSON here.
-- This is a read-only mirror: no source-side writes, no credentials stored.

ALTER TABLE backup_sources DROP CONSTRAINT IF EXISTS backup_sources_platform_check;
ALTER TABLE backup_sources ADD CONSTRAINT backup_sources_platform_check
  CHECK (platform = ANY (ARRAY['veeam'::text, 'veeam_push'::text, 'proxmox'::text, 'synology_abb'::text]));

ALTER TABLE backup_sources DROP CONSTRAINT IF EXISTS backup_sources_auth_type_check;
ALTER TABLE backup_sources ADD CONSTRAINT backup_sources_auth_type_check
  CHECK (auth_type = ANY (ARRAY['token'::text, 'password'::text, 'ingest'::text]));

-- Machine tokens for push ingestion. Only the SHA-256 hash is stored.
CREATE TABLE IF NOT EXISTS ingest_tokens (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  source_id   uuid NOT NULL REFERENCES backup_sources(id) ON DELETE CASCADE,
  label       text NOT NULL DEFAULT '',
  token_hash  text NOT NULL UNIQUE,
  created_at  timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz,
  revoked     boolean NOT NULL DEFAULT false
);
CREATE INDEX IF NOT EXISTS ingest_tokens_source ON ingest_tokens(source_id) WHERE NOT revoked;

ALTER TABLE veeam_jobs ADD COLUMN IF NOT EXISTS backup_server_id text NOT NULL DEFAULT '';
ALTER TABLE veeam_jobs ADD COLUMN IF NOT EXISTS schedule_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE veeam_jobs ADD COLUMN IF NOT EXISTS last_result_raw integer;

ALTER TABLE backup_sessions ADD COLUMN IF NOT EXISTS backup_server text NOT NULL DEFAULT '';
ALTER TABLE backup_sessions ADD COLUMN IF NOT EXISTS job_name text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS backup_sessions_veeam_lookup
  ON backup_sessions(source_id, backup_server, started_at DESC);
