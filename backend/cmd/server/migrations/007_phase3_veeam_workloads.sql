-- Phase 3f-2: surface Veeam push jobs as workloads (option A).
--
-- C.Backup.Model.JobSessions has no VM/object identity, so the finest reliable
-- granularity without touching the collector is one workload per backup job.
-- Each job gets a stable synthetic vmid (900001+) so sessions link exactly like
-- Proxmox/ABB: backup_workloads.vmid = backup_sessions.vmid.

CREATE SEQUENCE IF NOT EXISTS veeam_wl_vmid_seq START WITH 900001;

ALTER TABLE veeam_jobs ADD COLUMN IF NOT EXISTS wl_vmid integer;
ALTER TABLE veeam_jobs ADD COLUMN IF NOT EXISTS server_name text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS veeam_jobs_source_wl_vmid
  ON veeam_jobs(source_id, wl_vmid) WHERE wl_vmid IS NOT NULL;

-- Backfill existing inventory: allocate a vmid per job, then materialise one
-- workload row per job and relink the sessions already ingested.
WITH n AS (
  SELECT id, nextval('veeam_wl_vmid_seq') AS v FROM veeam_jobs
  WHERE wl_vmid IS NULL ORDER BY id
)
UPDATE veeam_jobs j SET wl_vmid = n.v FROM n WHERE j.id = n.id;

INSERT INTO backup_workloads(source_id,vmid,name,node,kind,status,template,last_seen,ext_id)
SELECT source_id, wl_vmid, name,
       CASE WHEN server_name <> '' THEN server_name ELSE backup_server END,
       'veeam_job', coalesce(last_result,''), false, now(), uid
FROM veeam_jobs WHERE wl_vmid IS NOT NULL
ON CONFLICT (source_id,vmid) DO UPDATE SET
  name=excluded.name, node=excluded.node, kind=excluded.kind,
  status=excluded.status, last_seen=now(), ext_id=excluded.ext_id;

UPDATE backup_sessions s
SET vmid = j.wl_vmid, workload_id = w.id
FROM veeam_jobs j
JOIN backup_workloads w ON w.source_id = j.source_id AND w.vmid = j.wl_vmid
WHERE s.source_id = j.source_id
  AND s.job_name <> ''
  AND s.job_name = j.name
  AND (j.backup_server_id = s.backup_server OR j.backup_server = s.backup_server)
  AND s.vmid = 0;
