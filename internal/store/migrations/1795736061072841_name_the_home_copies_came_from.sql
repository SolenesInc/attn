ALTER TABLE session_runtime RENAME COLUMN home_daemon_id TO home_id;
CREATE TABLE notebook_folders ( id TEXT PRIMARY KEY, folder_key TEXT NOT NULL, root_path TEXT NOT NULL UNIQUE, created_at TEXT NOT NULL );
