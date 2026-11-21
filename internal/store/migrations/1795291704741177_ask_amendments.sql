CREATE TABLE ask_amendments (
  ask_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  markdown TEXT NOT NULL,
  amended_at TEXT NOT NULL,
  PRIMARY KEY (ask_id, seq)
);

CREATE TRIGGER ask_amendments_die_with_their_ask AFTER DELETE ON asks
BEGIN
  DELETE FROM ask_amendments WHERE ask_id = OLD.id;
END;
