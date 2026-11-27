ALTER TABLE asks ADD COLUMN host TEXT NOT NULL DEFAULT '';
ALTER TABLE asks ADD COLUMN cwd TEXT NOT NULL DEFAULT '';
UPDATE asks SET
  host = COALESCE((SELECT host FROM sessions WHERE id = asks.session_id), (SELECT id FROM machines WHERE transport = 'self')),
  cwd = COALESCE((SELECT directory FROM sessions WHERE id = asks.session_id), base_dir);
CREATE TRIGGER machine_required_asks_insert BEFORE INSERT ON asks WHEN NEW.host = '' BEGIN SELECT RAISE(ABORT, 'asks.host must name a machine'); END;
CREATE TRIGGER machine_required_asks_update BEFORE UPDATE ON asks WHEN NEW.host = '' BEGIN SELECT RAISE(ABORT, 'asks.host must name a machine'); END;
