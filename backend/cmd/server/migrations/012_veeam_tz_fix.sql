-- Fix collector timezone bug (v1.0.3: TODATETIMEOFFSET(x, tzoffset) stamped UTC
-- values with +07:00, so every Veeam timestamp landed 7 hours early).
-- Shift existing rows forward by 7 hours, once. Guarded so a re-run is a no-op.

DO $$
DECLARE
    shifted_jobs int;
    shifted_sess int;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM schema_migrations WHERE version = '012_veeam_tz_fix.sql') THEN

        UPDATE backup_sessions s
           SET started_at = s.started_at + interval '7 hours',
               ended_at   = CASE WHEN s.ended_at IS NULL THEN NULL
                                 ELSE s.ended_at + interval '7 hours' END
          FROM backup_sources b
         WHERE b.id = s.source_id AND b.platform = 'veeam_push'
           AND s.started_at IS NOT NULL;
        GET DIAGNOSTICS shifted_sess = ROW_COUNT;

        UPDATE veeam_jobs j
           SET last_run = j.last_run + interval '7 hours'
          FROM backup_sources b
         WHERE b.id = j.source_id AND b.platform = 'veeam_push'
           AND j.last_run IS NOT NULL;
        GET DIAGNOSTICS shifted_jobs = ROW_COUNT;

        RAISE NOTICE 'tz fix: shifted % job rows, % session rows', shifted_jobs, shifted_sess;

        INSERT INTO schema_migrations (version) VALUES ('012_veeam_tz_fix.sql');
    ELSE
        RAISE NOTICE 'tz fix: already applied, skipping';
    END IF;
END $$;
