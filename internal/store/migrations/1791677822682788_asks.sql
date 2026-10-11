CREATE TABLE asks (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    address TEXT NOT NULL,
    subject TEXT NOT NULL,
    markdown TEXT NOT NULL,
    base_dir TEXT NOT NULL,
    created_at TEXT NOT NULL,
    closed_at TEXT NOT NULL DEFAULT '',
    close_reason TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_asks_open ON asks(created_at) WHERE closed_at = '';
