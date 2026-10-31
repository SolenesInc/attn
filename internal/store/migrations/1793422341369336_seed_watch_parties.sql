ALTER TABLE garden_seed_watches RENAME COLUMN watcher_session_id TO watcher;
UPDATE OR IGNORE garden_seed_watches SET watcher = COALESCE(
  (SELECT 'member:' || member_key FROM sessions WHERE sessions.id = garden_seed_watches.watcher AND member_key != ''),
  'session:' || watcher);
DELETE FROM garden_seed_watches WHERE watcher NOT LIKE 'session:%' AND watcher NOT LIKE 'member:%';
