CREATE TABLE ask_drafts (
  ask_id TEXT NOT NULL PRIMARY KEY,
  generation INTEGER NOT NULL,
  draft TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TRIGGER ask_drafts_die_with_their_ask AFTER DELETE ON asks
BEGIN
  DELETE FROM ask_drafts WHERE ask_id = OLD.id;
END;
