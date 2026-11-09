DELETE FROM settings WHERE key = 'codex_shared_enabled';
UPDATE automation_definitions
SET spec_json = json_remove(spec_json, '$.launch.executable')
WHERE lower(json_extract(spec_json, '$.launch.driver')) = 'codex'
  AND json_extract(spec_json, '$.launch.executable') IS NOT NULL;
