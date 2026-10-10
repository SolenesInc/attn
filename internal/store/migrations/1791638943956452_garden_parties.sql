ALTER TABLE garden_seed_watches RENAME COLUMN watcher_session_id TO watcher;
ALTER TABLE delegation_operations ADD COLUMN dispatcher TEXT NOT NULL DEFAULT '';
ALTER TABLE delegation_operations ADD COLUMN handover_tender TEXT NOT NULL DEFAULT '';
