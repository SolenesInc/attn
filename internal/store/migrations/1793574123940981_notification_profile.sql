ALTER TABLE notifications ADD COLUMN profile_id TEXT NOT NULL DEFAULT '';

DELETE FROM notifications WHERE kind = 'notebook_sync_conflict';
