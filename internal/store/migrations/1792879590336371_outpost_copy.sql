ALTER TABLE sessions ADD COLUMN host TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN copied_from TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions DROP COLUMN endpoint_id;

CREATE TABLE outpost_outbox (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  host TEXT NOT NULL,
  tbl TEXT NOT NULL,
  key TEXT NOT NULL,
  UNIQUE (tbl, key)
);
CREATE INDEX outpost_outbox_host ON outpost_outbox (host, id);

CREATE TRIGGER outpost_copy_session_insert AFTER INSERT ON sessions
WHEN NEW.host != '' AND EXISTS (SELECT 1 FROM outposts WHERE id = NEW.host)
BEGIN
  DELETE FROM outpost_outbox WHERE tbl = 'sessions' AND key = NEW.id;
  INSERT INTO outpost_outbox (host, tbl, key) VALUES (NEW.host, 'sessions', NEW.id);
END;

CREATE TRIGGER outpost_copy_session_update AFTER UPDATE ON sessions
WHEN NEW.host != '' AND EXISTS (SELECT 1 FROM outposts WHERE id = NEW.host)
BEGIN
  DELETE FROM outpost_outbox WHERE tbl = 'sessions' AND key = NEW.id;
  INSERT INTO outpost_outbox (host, tbl, key) VALUES (NEW.host, 'sessions', NEW.id);
END;

CREATE TRIGGER outpost_copy_session_delete AFTER DELETE ON sessions
WHEN OLD.host != '' AND EXISTS (SELECT 1 FROM outposts WHERE id = OLD.host)
BEGIN
  DELETE FROM outpost_outbox WHERE tbl = 'sessions' AND key = OLD.id;
  INSERT INTO outpost_outbox (host, tbl, key) VALUES (OLD.host, 'sessions', OLD.id);
END;

CREATE TRIGGER outpost_copy_binding_insert AFTER INSERT ON terminal_bindings
WHEN EXISTS (SELECT 1 FROM sessions s JOIN outposts o ON o.id = s.host WHERE s.id = NEW.session_id)
BEGIN
  DELETE FROM outpost_outbox WHERE tbl = 'terminal_bindings' AND key = NEW.terminal_id;
  INSERT INTO outpost_outbox (host, tbl, key) SELECT host, 'terminal_bindings', NEW.terminal_id FROM sessions WHERE id = NEW.session_id;
END;

CREATE TRIGGER outpost_copy_binding_update AFTER UPDATE ON terminal_bindings
WHEN EXISTS (SELECT 1 FROM sessions s JOIN outposts o ON o.id = s.host WHERE s.id IN (OLD.session_id, NEW.session_id))
BEGIN
  DELETE FROM outpost_outbox WHERE tbl = 'terminal_bindings' AND key = NEW.terminal_id;
  INSERT INTO outpost_outbox (host, tbl, key) SELECT s.host, 'terminal_bindings', NEW.terminal_id FROM sessions s JOIN outposts o ON o.id = s.host WHERE s.id IN (OLD.session_id, NEW.session_id) LIMIT 1;
END;

CREATE TRIGGER outpost_copy_binding_delete AFTER DELETE ON terminal_bindings
WHEN EXISTS (SELECT 1 FROM sessions s JOIN outposts o ON o.id = s.host WHERE s.id = OLD.session_id)
BEGIN
  DELETE FROM outpost_outbox WHERE tbl = 'terminal_bindings' AND key = OLD.terminal_id;
  INSERT INTO outpost_outbox (host, tbl, key) SELECT host, 'terminal_bindings', OLD.terminal_id FROM sessions WHERE id = OLD.session_id;
END;
