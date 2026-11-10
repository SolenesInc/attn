ALTER TABLE file_activity ADD COLUMN host TEXT NOT NULL DEFAULT '';
CREATE TABLE file_activity_by_host (
  path TEXT NOT NULL,
  source TEXT NOT NULL,
  session_id TEXT,
  last_at TEXT NOT NULL,
  count INTEGER NOT NULL DEFAULT 1,
  host TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (host, path, source)
);
INSERT INTO file_activity_by_host SELECT * FROM file_activity;
DROP TABLE file_activity;
ALTER TABLE file_activity_by_host RENAME TO file_activity;
CREATE INDEX idx_file_activity_last_at ON file_activity(last_at DESC);

ALTER TABLE markdown_annotation_drafts ADD COLUMN host TEXT NOT NULL DEFAULT '';
CREATE TABLE markdown_annotation_drafts_by_host (
  path TEXT NOT NULL,
  annotations_json TEXT NOT NULL,
  generation INTEGER NOT NULL,
  tombstone_generation INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  host TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (host, path)
);
INSERT INTO markdown_annotation_drafts_by_host SELECT * FROM markdown_annotation_drafts;
DROP TABLE markdown_annotation_drafts;
ALTER TABLE markdown_annotation_drafts_by_host RENAME TO markdown_annotation_drafts;
