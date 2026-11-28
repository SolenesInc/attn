CREATE TABLE view_drafts (
  host TEXT NOT NULL,
  path TEXT NOT NULL,
  row TEXT NOT NULL,
  part TEXT NOT NULL,
  answer_json TEXT NOT NULL,
  PRIMARY KEY (host, path, row, part)
);

CREATE TABLE view_sends (
  host TEXT NOT NULL,
  path TEXT NOT NULL,
  sent_at TEXT NOT NULL,
  responses TEXT NOT NULL,
  told INTEGER NOT NULL,
  PRIMARY KEY (host, path)
);
