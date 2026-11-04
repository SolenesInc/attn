CREATE TABLE github_hosts (host TEXT PRIMARY KEY);

INSERT INTO github_hosts (host) VALUES ('github.com');

INSERT OR IGNORE INTO github_hosts (host)
SELECT host FROM prs WHERE host != '';

INSERT OR IGNORE INTO github_hosts (host)
SELECT substr(url, 9, instr(substr(url, 9), '/') - 1) FROM repo_merged_branches
WHERE url LIKE 'https://%/%';

INSERT OR IGNORE INTO github_hosts (host)
SELECT substr(url, 9, instr(substr(url, 9), '/') - 1) FROM session_pull_requests
WHERE url LIKE 'https://%/%';
