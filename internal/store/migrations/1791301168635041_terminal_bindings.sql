CREATE TABLE terminal_bindings (
    terminal_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE
);
CREATE INDEX idx_terminal_bindings_session ON terminal_bindings(session_id);

INSERT OR REPLACE INTO terminal_bindings (terminal_id, session_id)
SELECT runtime_id, session_id FROM desktop_panes
WHERE kind = 'agent' AND runtime_id != '' AND session_id != '';

INSERT OR IGNORE INTO terminal_bindings (terminal_id, session_id)
SELECT id, id FROM sessions
WHERE closed_at = '' AND NOT EXISTS (
    SELECT 1 FROM terminal_bindings WHERE session_id = sessions.id
);
