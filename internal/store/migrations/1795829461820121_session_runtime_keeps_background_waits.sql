ALTER TABLE session_runtime ADD COLUMN background TEXT NOT NULL DEFAULT '';
ALTER TABLE session_runtime ADD COLUMN notification_stop INTEGER NOT NULL DEFAULT 0;
