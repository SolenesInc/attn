ALTER TABLE session_runtime ADD COLUMN home_daemon_id TEXT NOT NULL DEFAULT '';
ALTER TABLE session_runtime ADD COLUMN live INTEGER NOT NULL DEFAULT 0;
ALTER TABLE session_runtime ADD COLUMN agent TEXT NOT NULL DEFAULT '';
ALTER TABLE session_runtime ADD COLUMN conversation_native_id TEXT NOT NULL DEFAULT '';
ALTER TABLE session_runtime ADD COLUMN conversation_transcript_path TEXT NOT NULL DEFAULT '';

UPDATE session_runtime
SET live = EXISTS(SELECT 1 FROM sessions s WHERE s.id = session_id AND s.closed_at = '' AND NOT EXISTS (SELECT 1 FROM session_exit_screens e WHERE e.session_id = s.id)),
    home_daemon_id = COALESCE((SELECT copied_from FROM sessions WHERE id = session_id), ''),
    agent = COALESCE((SELECT agent FROM sessions WHERE id = session_id), ''),
    conversation_native_id = COALESCE((SELECT resume_session_id FROM sessions WHERE id = session_id), ''),
    conversation_transcript_path = COALESCE((SELECT transcript_path FROM sessions WHERE id = session_id), '');
