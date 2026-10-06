CREATE TABLE command_usage (
    profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    command_id TEXT NOT NULL,
    score REAL NOT NULL,
    last_used_at TEXT NOT NULL,
    PRIMARY KEY(profile_id, command_id)
);
