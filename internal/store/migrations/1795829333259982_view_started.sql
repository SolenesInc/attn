CREATE TABLE view_started (
    profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    view TEXT NOT NULL,
    machine_id TEXT NOT NULL,
    line TEXT NOT NULL,
    script_sha256 TEXT NOT NULL,
    started_at TEXT NOT NULL,
    PRIMARY KEY (profile_id, view, machine_id, line)
);
