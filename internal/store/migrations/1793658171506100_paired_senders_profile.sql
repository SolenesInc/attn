CREATE TABLE paired_senders_by_profile (
 id TEXT PRIMARY KEY, profile_id TEXT NOT NULL, name TEXT NOT NULL COLLATE NOCASE, token_hash TEXT NOT NULL UNIQUE,
 created_at TEXT NOT NULL, last_used_at TEXT NOT NULL DEFAULT '',
 UNIQUE (profile_id, name)
);
INSERT INTO paired_senders_by_profile (id, profile_id, name, token_hash, created_at, last_used_at)
SELECT p.id, live.id, p.name, p.token_hash, p.created_at, p.last_used_at
FROM paired_senders p JOIN (SELECT id FROM profiles WHERE deleted_at = '') live
WHERE (SELECT COUNT(*) FROM profiles WHERE deleted_at = '') = 1;
DROP TABLE paired_senders;
ALTER TABLE paired_senders_by_profile RENAME TO paired_senders;
