ALTER TABLE settings ADD COLUMN copied_from TEXT NOT NULL DEFAULT '';
CREATE TABLE settings_with_copy (
  key TEXT NOT NULL,
  value TEXT,
  copied_from TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (key, copied_from)
);
INSERT INTO settings_with_copy SELECT * FROM settings;
DROP TABLE settings;
ALTER TABLE settings_with_copy RENAME TO settings;

CREATE TABLE outpost_outbox_by_host (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  host TEXT NOT NULL,
  tbl TEXT NOT NULL,
  key TEXT NOT NULL,
  UNIQUE (host, tbl, key)
);
INSERT INTO outpost_outbox_by_host SELECT * FROM outpost_outbox;
DROP TRIGGER outpost_copy_session_insert;
DROP TRIGGER outpost_copy_session_update;
DROP TRIGGER outpost_copy_session_delete;
DROP TRIGGER outpost_copy_binding_insert;
DROP TRIGGER outpost_copy_binding_update;
DROP TRIGGER outpost_copy_binding_delete;
DROP TABLE outpost_outbox;
ALTER TABLE outpost_outbox_by_host RENAME TO outpost_outbox;
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

CREATE TRIGGER outpost_copy_setting_insert AFTER INSERT ON settings
WHEN NEW.copied_from = '' AND attn_home_setting(NEW.key)
BEGIN
  DELETE FROM outpost_outbox WHERE tbl = 'settings' AND key = NEW.key;
  INSERT INTO outpost_outbox(host, tbl, key) SELECT id, 'settings', NEW.key FROM outposts;
END;

CREATE TRIGGER outpost_copy_setting_update AFTER UPDATE ON settings
WHEN NEW.copied_from = '' AND attn_home_setting(NEW.key)
BEGIN
  DELETE FROM outpost_outbox WHERE tbl = 'settings' AND key = NEW.key;
  INSERT INTO outpost_outbox(host, tbl, key) SELECT id, 'settings', NEW.key FROM outposts;
END;

CREATE TRIGGER outpost_copy_setting_delete AFTER DELETE ON settings
WHEN OLD.copied_from = '' AND attn_home_setting(OLD.key)
BEGIN
  DELETE FROM outpost_outbox WHERE tbl = 'settings' AND key = OLD.key;
  INSERT INTO outpost_outbox(host, tbl, key) SELECT id, 'settings', OLD.key FROM outposts;
END;

CREATE TRIGGER refuse_copied_sessions_insert BEFORE INSERT ON sessions
WHEN (NEW.copied_from != '' OR EXISTS (SELECT 1 FROM sessions WHERE id = NEW.id AND copied_from != ''))
BEGIN
  SELECT attn_require_copy_applier('sessions', NEW.id);
END;

CREATE TRIGGER refuse_copied_sessions_update BEFORE UPDATE ON sessions
WHEN (OLD.copied_from != '' OR NEW.copied_from != '')
BEGIN
  SELECT attn_require_copy_applier('sessions', OLD.id);
END;

CREATE TRIGGER refuse_copied_sessions_delete BEFORE DELETE ON sessions
WHEN (OLD.copied_from != '')
BEGIN
  SELECT attn_require_copy_applier('sessions', OLD.id);
END;

CREATE TRIGGER refuse_copied_terminal_bindings_insert BEFORE INSERT ON terminal_bindings
WHEN (EXISTS (SELECT 1 FROM sessions WHERE id = NEW.session_id AND copied_from != '') OR EXISTS (SELECT 1 FROM terminal_bindings b JOIN sessions s ON s.id = b.session_id WHERE b.terminal_id = NEW.terminal_id AND s.copied_from != ''))
BEGIN
  SELECT attn_require_copy_applier('terminal_bindings', NEW.terminal_id);
END;

CREATE TRIGGER refuse_copied_terminal_bindings_update BEFORE UPDATE ON terminal_bindings
WHEN (EXISTS (SELECT 1 FROM sessions WHERE id = OLD.session_id AND copied_from != '') OR EXISTS (SELECT 1 FROM sessions WHERE id = NEW.session_id AND copied_from != ''))
BEGIN
  SELECT attn_require_copy_applier('terminal_bindings', OLD.terminal_id);
END;

CREATE TRIGGER refuse_copied_terminal_bindings_delete BEFORE DELETE ON terminal_bindings
WHEN (EXISTS (SELECT 1 FROM sessions WHERE id = OLD.session_id AND copied_from != ''))
BEGIN
  SELECT attn_require_copy_applier('terminal_bindings', OLD.terminal_id);
END;

CREATE TRIGGER refuse_copied_settings_insert BEFORE INSERT ON settings
WHEN (NEW.copied_from != '' OR EXISTS (SELECT 1 FROM settings WHERE key = NEW.key AND copied_from != ''))
BEGIN
  SELECT attn_require_copy_applier('settings', NEW.key);
END;

CREATE TRIGGER refuse_copied_settings_update BEFORE UPDATE ON settings
WHEN (OLD.copied_from != '' OR NEW.copied_from != '')
BEGIN
  SELECT attn_require_copy_applier('settings', OLD.key);
END;

CREATE TRIGGER refuse_copied_settings_delete BEFORE DELETE ON settings
WHEN (OLD.copied_from != '')
BEGIN
  SELECT attn_require_copy_applier('settings', OLD.key);
END;

