-- 008: node alias table
--
-- The Node column showed raw identifiers (IPs, Veeam GUIDs, empty strings)
-- because reverse DNS is not available for those hosts. Operators want the FQDN
-- (e.g. backup01.example.com). Storing the map in a table keeps it editable with a
-- plain UPDATE instead of a rebuild, and lets the loader fall back to DNS for
-- anything not listed here.

CREATE TABLE IF NOT EXISTS node_aliases (
    raw        text PRIMARY KEY,   -- what the adapter reports: IP, GUID, hostname
    alias      text NOT NULL,      -- what the UI should display
    note       text,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Seed your own aliases with plain INSERTs, for example:
--   INSERT INTO node_aliases (raw, alias, note) VALUES
--     ('192.0.2.10', 'backup01.example.com', 'Veeam backup server'),
--     ('00000000-0000-0000-0000-000000000000', 'backup01.example.com', 'Veeam db_instance_id'),
--     ('nas01', 'nas01.example.com', 'Synology ABB')
--   ON CONFLICT (raw) DO NOTHING;
-- Both the IP and the Veeam db_instance_id GUID are worth seeding because jobs
-- and sessions carry different identifiers.

-- Backfill existing rows so the table is correct before the next poll.
UPDATE backup_workloads SET node = a.alias
  FROM node_aliases a WHERE backup_workloads.node = a.raw;
UPDATE veeam_jobs SET backup_server = a.alias, server_name = a.alias
  FROM node_aliases a WHERE veeam_jobs.backup_server = a.raw;
