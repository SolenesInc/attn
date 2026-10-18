CREATE TABLE outposts (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  address TEXT NOT NULL UNIQUE,
  host_key TEXT,
  daemon_id TEXT UNIQUE,
  added_at TEXT NOT NULL,
  paired_at TEXT
);
