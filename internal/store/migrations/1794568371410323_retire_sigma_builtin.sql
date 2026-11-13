INSERT INTO plugin_server_owners (agent, plugin_name)
SELECT 'sigma', 'sigma'
WHERE EXISTS (SELECT 1 FROM sessions WHERE link = 'sigma-server')
   OR EXISTS (SELECT 1 FROM terminal_views WHERE link = 'sigma-server');

DELETE FROM settings WHERE key = 'sigma_enabled';
