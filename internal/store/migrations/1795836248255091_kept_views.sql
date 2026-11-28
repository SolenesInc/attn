CREATE TABLE views (
    profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    version INTEGER NOT NULL,
    machine_id TEXT NOT NULL,
    folder TEXT NOT NULL DEFAULT '',
    folder_version INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (profile_id, name)
);

CREATE TABLE view_versions (
    profile_id TEXT NOT NULL,
    name TEXT NOT NULL,
    version INTEGER NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    restored_from INTEGER NOT NULL DEFAULT 0,
    saved_at TEXT NOT NULL,
    saved_by TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (profile_id, name, version)
);

CREATE TABLE view_files (
    profile_id TEXT NOT NULL,
    name TEXT NOT NULL,
    version INTEGER NOT NULL,
    file TEXT NOT NULL,
    content BLOB NOT NULL,
    PRIMARY KEY (profile_id, name, version, file)
);
