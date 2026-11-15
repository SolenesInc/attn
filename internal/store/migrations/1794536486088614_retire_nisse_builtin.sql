INSERT INTO plugin_server_owners (agent, plugin_name)
SELECT 'nisse', 'nisse'
WHERE EXISTS (SELECT 1 FROM sessions WHERE link = 'nisse-server')
   OR EXISTS (SELECT 1 FROM terminal_views WHERE link = 'nisse-server');

DELETE FROM settings WHERE key = 'nisse_enabled';
