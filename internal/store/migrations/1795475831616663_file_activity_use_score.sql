ALTER TABLE file_activity ADD COLUMN use_score REAL NOT NULL DEFAULT 0;
UPDATE file_activity SET use_score = count;
