CREATE TABLE recent_locations_by_host (
	host TEXT NOT NULL DEFAULT '',
	path TEXT NOT NULL,
	last_seen TEXT NOT NULL,
	use_count INTEGER NOT NULL DEFAULT 1,
	repo_root TEXT,
	PRIMARY KEY (host, path)
);
INSERT INTO recent_locations_by_host (path, last_seen, use_count)
	SELECT path, last_seen, use_count FROM recent_locations;
DROP TABLE recent_locations;
ALTER TABLE recent_locations_by_host RENAME TO recent_locations;
