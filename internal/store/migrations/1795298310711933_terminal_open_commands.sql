CREATE TABLE terminal_open_commands (
  terminal_id TEXT PRIMARY KEY,
  opener_session_id TEXT NOT NULL,
  command TEXT NOT NULL,
  created_at TEXT NOT NULL
);
