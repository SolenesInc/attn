INSERT INTO profile_settings (profile_id, key, value)
SELECT profiles.id, 'notebook.root', trim(settings.value)
FROM profiles, settings
WHERE profiles.deleted_at <> ''
  AND settings.key = 'notebook.root'
  AND trim(settings.value) <> ''
  AND NOT EXISTS (
    SELECT 1 FROM profile_settings own
    WHERE own.profile_id = profiles.id AND own.key IN ('notebook.root', 'notebook.profile_root')
  );
