ALTER TABLE pull_request_watches ADD COLUMN readiness_state TEXT NOT NULL DEFAULT '';
ALTER TABLE pull_request_watches ADD COLUMN readiness_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE pull_request_watches ADD COLUMN settling_until TEXT NOT NULL DEFAULT '';
ALTER TABLE pull_request_watches ADD COLUMN last_checked_at TEXT NOT NULL DEFAULT '';

ALTER TABLE session_pull_requests DROP COLUMN readiness_state;
ALTER TABLE session_pull_requests DROP COLUMN readiness_reason;
ALTER TABLE session_pull_requests DROP COLUMN settling_until;
ALTER TABLE session_pull_requests DROP COLUMN watch_health;
ALTER TABLE session_pull_requests DROP COLUMN watch_error;
ALTER TABLE session_pull_requests DROP COLUMN watch_last_checked_at;
