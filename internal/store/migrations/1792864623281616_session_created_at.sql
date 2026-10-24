ALTER TABLE sessions ADD COLUMN created_at TEXT NOT NULL DEFAULT '';

DROP VIEW sessions_with_runtime;

CREATE VIEW sessions_with_runtime AS
SELECT
    s.*,
    COALESCE(r.state, 'launching') AS state,
    COALESCE(r.state_since, s.created_at) AS state_since,
    COALESCE(r.state_updated_at, s.created_at) AS state_updated_at,
    COALESCE(r.last_seen, s.created_at) AS last_seen,
    r.branch AS branch,
    COALESCE(r.is_worktree, 0) AS is_worktree,
    r.main_repo AS main_repo,
    COALESCE(r.repository, '') AS repository,
    COALESCE(r.agent_metadata, '') AS agent_metadata,
    COALESCE(r.agent_driver_plugin_name, '') AS agent_driver_plugin_name,
    COALESCE(r.agent_driver_run_id, '') AS agent_driver_run_id,
    COALESCE(r.agent_driver_report_seq, 0) AS agent_driver_report_seq,
    COALESCE(r.agent_driver_transcript_path, '') AS agent_driver_transcript_path,
    COALESCE(r.turn_settled_at, '') AS turn_settled_at,
    COALESCE(r.turn_opened_at, '') AS turn_opened_at,
    COALESCE(r.turn_snoozed_until, '') AS turn_snoozed_until,
    COALESCE(r.turn_agent_at, '') AS turn_agent_at,
    COALESCE(r.turn_requested_at, '') AS turn_requested_at,
    r.last_model_request_at AS last_model_request_at,
    COALESCE(r.harness_title, '') AS harness_title,
    COALESCE(r.session_cost_json, '') AS session_cost_json,
    COALESCE(r.pending_switch, '') AS pending_switch,
    COALESCE(r.pull_requests, '') AS pull_requests
FROM sessions s
LEFT JOIN session_runtime r ON r.session_id = s.id;
