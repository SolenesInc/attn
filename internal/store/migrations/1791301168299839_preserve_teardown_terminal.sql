ALTER TABLE session_teardown_tombstones ADD COLUMN driver_terminal_id TEXT NOT NULL DEFAULT '';

UPDATE session_teardown_tombstones
SET driver_terminal_id = COALESCE((
    SELECT runtime_id FROM desktop_panes
    WHERE session_id = session_teardown_tombstones.session_id
    ORDER BY created_at DESC, pane_id DESC
    LIMIT 1
), '');
