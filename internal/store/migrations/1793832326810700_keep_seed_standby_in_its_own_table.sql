CREATE TABLE seed_standby (
	seed_id TEXT PRIMARY KEY,
	profile_id TEXT NOT NULL,
	tender TEXT NOT NULL,
	note TEXT NOT NULL,
	set_at TEXT NOT NULL,
	set_by TEXT NOT NULL
);
