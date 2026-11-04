-- 013_abb_retire_orphans.sql
-- Tandai workload ABB yang tidak punya jejak backup sama sekali sebagai 'removed'
-- (state ini sudah dipakai aplikasi: dikecualikan dari daftar workload, KPI, dan
-- hitungan sumber — lihat data.go wlSQL, sources.go counts, pve.go).
--
-- Kenapa bukan DELETE: baris tetap ada sebagai jejak audit, dan kalau host-nya
-- kelak ditambahkan lagi ke ABB, sync akan meng-upsert baris yang sama dengan
-- status dari sesi terbaru sehingga otomatis pulih.
--
-- Definisi yatim dipakai di sini (semua kondisi harus benar):
--   1. platform sumber = synology_abb
--   2. tidak punya satu pun backup_sessions
--   3. tidak punya satu pun abb_events yang device_name-nya cocok
--   4. device_id-nya tidak terdaftar di devices pada abb_tasks mana pun
-- Dengan begitu migrasi ini idempoten dan tidak akan mengenai host aktif.

BEGIN;

UPDATE backup_workloads w
   SET status = 'removed'
  FROM backup_sources s
 WHERE s.id = w.source_id
   AND s.platform = 'synology_abb'
   AND w.status <> 'removed'
   AND NOT EXISTS (SELECT 1 FROM backup_sessions x WHERE x.workload_id = w.id)
   AND NOT EXISTS (SELECT 1 FROM abb_events e
                    WHERE e.source_id = w.source_id
                      AND lower(e.device_name) = lower(w.name))
   AND NOT EXISTS (SELECT 1 FROM abb_tasks t,
                          jsonb_array_elements(coalesce(t.raw->'devices','[]'::jsonb)) d
                    WHERE t.source_id = w.source_id
                      AND (d->>'device_id')::int = w.vmid);

INSERT INTO schema_migrations(version) VALUES ('013_abb_retire_orphans.sql')
ON CONFLICT (version) DO NOTHING;

COMMIT;
