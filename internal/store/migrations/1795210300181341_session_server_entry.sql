ALTER TABLE session_runtime ADD COLUMN server_entry INTEGER NOT NULL DEFAULT 0;
ALTER TABLE session_runtime ADD COLUMN server_usage_baseline TEXT NOT NULL DEFAULT '';
