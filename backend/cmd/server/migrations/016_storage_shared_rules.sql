CREATE TABLE IF NOT EXISTS storage_shared_rules (
  id serial PRIMARY KEY,
  name_pattern text NOT NULL UNIQUE,
  note text NOT NULL DEFAULT ''
);
INSERT INTO storage_shared_rules(name_pattern,note) VALUES ('%qnap%','QNAP dipakai bersama oleh beberapa backup server') ON CONFLICT DO NOTHING;
UPDATE storage_pools sp SET shared=true WHERE kind='veeam_repo' AND EXISTS (SELECT 1 FROM storage_shared_rules r WHERE sp.name ILIKE r.name_pattern);
