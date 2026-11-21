INSERT INTO settings (key, value, copied_from)
SELECT 'model_calls.agent', value, copied_from FROM settings
WHERE key = 'dictation.refinement_agent' AND TRIM(COALESCE(value, '')) != ''
ON CONFLICT (key, copied_from) DO UPDATE SET value = excluded.value
WHERE TRIM(COALESCE(settings.value, '')) = '';
DELETE FROM settings WHERE key = 'dictation.refinement_agent';
