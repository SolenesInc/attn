UPDATE profile_settings SET key = 'notebook.root' WHERE key = 'notebook.profile_root';

INSERT INTO profile_settings (profile_id, key, value)
SELECT profiles.id, 'notebook.root', trim(settings.value)
FROM profiles, settings
WHERE profiles.deleted_at = ''
  AND settings.key = 'notebook.root'
  AND trim(settings.value) <> ''
ON CONFLICT (profile_id, key) DO NOTHING;

DELETE FROM settings WHERE key = 'notebook.root';
