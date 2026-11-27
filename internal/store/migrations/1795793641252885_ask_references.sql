CREATE TABLE ask_references ( ask_id TEXT NOT NULL, amendment INTEGER NOT NULL, seq INTEGER NOT NULL, kind TEXT NOT NULL, text TEXT NOT NULL, host TEXT NOT NULL, path TEXT NOT NULL, line INTEGER NOT NULL, PRIMARY KEY (ask_id, amendment, seq) );
CREATE TRIGGER ask_references_die_with_their_ask AFTER DELETE ON asks BEGIN DELETE FROM ask_references WHERE ask_id = OLD.id; END;
CREATE TRIGGER machine_required_ask_references_insert BEFORE INSERT ON ask_references WHEN NEW.host = '' BEGIN SELECT RAISE(ABORT, 'ask_references.host must name a machine'); END;
CREATE TRIGGER machine_required_ask_references_update BEFORE UPDATE ON ask_references WHEN NEW.host = '' BEGIN SELECT RAISE(ABORT, 'ask_references.host must name a machine'); END;
