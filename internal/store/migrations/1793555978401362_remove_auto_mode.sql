DROP INDEX IF EXISTS idx_automode_denials_recent;
DROP INDEX IF EXISTS idx_automode_proposals_pending_ask;
DROP INDEX IF EXISTS idx_automode_proposals_state;
DROP TABLE automode_config;
DROP TABLE automode_denials;
DROP TABLE automode_proposals;
DELETE FROM settings WHERE key IN ('installed_bundled_plugins', 'automode_enabled_default', 'automode_denial_ledger_cursor');
