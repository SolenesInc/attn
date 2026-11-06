UPDATE automation_runs
SET launched_at = (SELECT launched_at FROM sessions WHERE sessions.id = automation_runs.session_id)
WHERE state = 'pending'
  AND launched_at = ''
  AND EXISTS (
    SELECT 1 FROM sessions
    WHERE sessions.id = automation_runs.session_id
      AND julianday(sessions.launched_at) >= julianday(automation_runs.created_at)
  );
