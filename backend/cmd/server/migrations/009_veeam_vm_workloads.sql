-- 009: per-VM (object-level) workloads for Veeam push sources.

-- Option A gave one workload per job because JobSessions carries no VM
-- identity. BackupTaskSessions does, so each VM gets its own workload row and
-- its own vmid space, well clear of both the hypervisor range and the
-- job-level range allocated by veeam_wl_vmid_seq (900001+).
CREATE SEQUENCE IF NOT EXISTS veeam_vm_vmid_seq START 910001;

-- Latest object-level run per VM, one row per VM. Deliberately NOT a session
-- table: the job-level rows in backup_sessions already carry the run KPI, and
-- adding object rows there would inflate every session count. vm_key is the
-- object UID when the collector reports one, else 'name:<lowercased name>', so
-- a rename keeps history and a hostname reused on two backup servers does not
-- merge into one workload.
CREATE TABLE IF NOT EXISTS veeam_vms (
    source_id    uuid NOT NULL REFERENCES backup_sources(id) ON DELETE CASCADE,
    vm_key       text NOT NULL,
    vm_name      text NOT NULL DEFAULT '',
    vmid         integer NOT NULL,
    job_name     text NOT NULL DEFAULT '',
    node         text NOT NULL DEFAULT '',
    last_run_at  timestamptz,
    last_status  text NOT NULL DEFAULT '',
    last_success timestamptz,
    last_reason  text NOT NULL DEFAULT '',
    last_size    bigint NOT NULL DEFAULT 0,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, vm_key),
    UNIQUE (source_id, vmid)
);

CREATE INDEX IF NOT EXISTS veeam_vms_vmid_idx ON veeam_vms (source_id, vmid);

-- The workload query excludes a job-level row once that job reports per-VM
-- rows; that lookup is by job name.
CREATE INDEX IF NOT EXISTS veeam_vms_job_idx ON veeam_vms (source_id, job_name);
