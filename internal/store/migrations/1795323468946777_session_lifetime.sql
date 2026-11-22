ALTER TABLE sessions ADD COLUMN lifetime TEXT NOT NULL DEFAULT 'terminal';
UPDATE sessions SET lifetime = 'server' WHERE link != '';
ALTER TABLE sessions DROP COLUMN link;
