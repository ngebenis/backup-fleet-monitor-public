-- One-time backfill: ABB sessions that failed only because of an abb_merge step are warnings.
UPDATE backup_sessions s SET status='warning', raw_status='MERGE'
FROM backup_sources b
WHERE b.id=s.source_id AND b.platform='synology_abb' AND s.status='failed' AND EXISTS (
  SELECT 1 FROM abb_events e WHERE e.source_id=s.source_id AND e.class_name='abb_merge' AND e.error_code<>0
   AND e.task_name=(s.message::jsonb->>'task')
   AND abs(extract(epoch FROM e.log_time - coalesce(s.ended_at,s.started_at)))<=120);
UPDATE backup_workloads w SET status=x.status
FROM (SELECT DISTINCT ON (s.workload_id) s.workload_id, s.status FROM backup_sessions s
      JOIN backup_sources b ON b.id=s.source_id AND b.platform='synology_abb'
      WHERE s.workload_id IS NOT NULL ORDER BY s.workload_id, coalesce(s.ended_at,s.started_at) DESC) x
WHERE w.id=x.workload_id;
