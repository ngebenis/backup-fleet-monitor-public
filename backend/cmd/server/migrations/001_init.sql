
CREATE TABLE roles (
  id serial PRIMARY KEY,
  name text UNIQUE NOT NULL,
  description text NOT NULL DEFAULT '',
  is_system boolean NOT NULL DEFAULT false
);
CREATE TABLE role_permissions (
  role_id int NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
  permission text NOT NULL,
  PRIMARY KEY (role_id, permission)
);
CREATE TABLE users (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  username text UNIQUE NOT NULL,
  display_name text NOT NULL DEFAULT '',
  password_hash text NOT NULL,
  role_id int NOT NULL REFERENCES roles(id),
  active boolean NOT NULL DEFAULT true,
  must_change_password boolean NOT NULL DEFAULT false,
  language text NOT NULL DEFAULT 'id' CHECK (language IN ('id','en')),
  theme text NOT NULL DEFAULT 'system' CHECK (theme IN ('light','dark','system')),
  timezone text NOT NULL DEFAULT 'Asia/Jakarta',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE user_sessions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash text UNIQUE NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  last_seen timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  ip text NOT NULL DEFAULT '',
  user_agent text NOT NULL DEFAULT '',
  revoked boolean NOT NULL DEFAULT false
);
CREATE INDEX ON user_sessions(user_id);
CREATE TABLE audit_logs (
  id bigserial PRIMARY KEY,
  ts timestamptz NOT NULL DEFAULT now(),
  actor_id uuid,
  actor_name text NOT NULL DEFAULT '',
  action text NOT NULL,
  resource_type text NOT NULL DEFAULT '',
  resource_id text NOT NULL DEFAULT '',
  outcome text NOT NULL,
  ip text NOT NULL DEFAULT '',
  user_agent text NOT NULL DEFAULT '',
  request_id text NOT NULL DEFAULT '',
  detail jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX ON audit_logs(ts DESC);
CREATE INDEX ON audit_logs(action);
CREATE FUNCTION audit_logs_immutable() RETURNS trigger AS $$
BEGIN RAISE EXCEPTION 'audit_logs is append-only'; END; $$ LANGUAGE plpgsql;
CREATE TRIGGER audit_logs_no_update BEFORE UPDATE OR DELETE ON audit_logs
  FOR EACH ROW EXECUTE FUNCTION audit_logs_immutable();
CREATE TABLE app_settings (
  key text PRIMARY KEY,
  value jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE backup_sources (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  platform text NOT NULL CHECK (platform IN ('veeam','proxmox','synology_abb')),
  base_url text NOT NULL,
  environment text NOT NULL DEFAULT '',
  poll_interval_seconds int NOT NULL DEFAULT 1800,
  enabled boolean NOT NULL DEFAULT true,
  connection_status text NOT NULL DEFAULT 'not_tested',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE service_heartbeat (
  service text PRIMARY KEY,
  last_beat timestamptz NOT NULL DEFAULT now(),
  version text NOT NULL DEFAULT ''
);
INSERT INTO roles(name,description,is_system) VALUES
 ('super_admin','Full access',true),
 ('administrator','Manage sources, alerts, reports, settings',true),
 ('backup_operator','View data, acknowledge alerts, export reports',true),
 ('auditor','Read only incl. audit log',true);
