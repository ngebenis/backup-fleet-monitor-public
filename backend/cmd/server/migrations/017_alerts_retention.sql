-- 017: alerting + notification channels, and controlled audit-log retention.

CREATE TABLE IF NOT EXISTS notification_channels (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name         text NOT NULL,
  kind         text NOT NULL CHECK (kind IN ('email','telegram','webhook')),
  config_enc   bytea NOT NULL,              -- AES-GCM(JSON config), same key as source credentials
  enabled      boolean NOT NULL DEFAULT true,
  last_status  text NOT NULL DEFAULT 'never' CHECK (last_status IN ('never','ok','error')),
  last_error   text NOT NULL DEFAULT '',
  last_sent_at timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS notification_channels_name_uq ON notification_channels(lower(name));

CREATE TABLE IF NOT EXISTS alerts (
  id              bigserial PRIMARY KEY,
  fingerprint     text NOT NULL,            -- stable identity of the condition, e.g. wl:<id>:failed
  type            text NOT NULL,            -- backup_failed | backup_stale | backup_never | storage_high | source_down
  severity        text NOT NULL CHECK (severity IN ('warning','critical')),
  source_id       uuid REFERENCES backup_sources(id) ON DELETE CASCADE,
  title           text NOT NULL,
  detail          text NOT NULL DEFAULT '',
  state           text NOT NULL DEFAULT 'open' CHECK (state IN ('open','acknowledged','resolved')),
  first_seen      timestamptz NOT NULL DEFAULT now(),
  last_seen       timestamptz NOT NULL DEFAULT now(),
  resolved_at     timestamptz,
  acknowledged_at timestamptz,
  acknowledged_by text NOT NULL DEFAULT '',
  notified_at     timestamptz,
  notify_count    int NOT NULL DEFAULT 0
);
-- At most one live alert per condition; a resolved one is history.
CREATE UNIQUE INDEX IF NOT EXISTS alerts_active_fp ON alerts(fingerprint) WHERE state <> 'resolved';
CREATE INDEX IF NOT EXISTS alerts_state_seen ON alerts(state, last_seen DESC);

-- audit_logs stays append-only, with one exception: retention may DELETE old rows,
-- but only inside a transaction that sets bm.audit_purge = 'on'. UPDATE is never allowed.
CREATE OR REPLACE FUNCTION audit_logs_immutable() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' AND current_setting('bm.audit_purge', true) = 'on' THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'audit_logs is append-only';
END; $$ LANGUAGE plpgsql;
