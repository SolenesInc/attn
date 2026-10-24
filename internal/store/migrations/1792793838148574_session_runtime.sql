CREATE TABLE session_runtime (
    session_id TEXT PRIMARY KEY,
    state TEXT NOT NULL DEFAULT 'launching',
    state_since TEXT NOT NULL DEFAULT '',
    state_updated_at TEXT NOT NULL DEFAULT '',
    last_seen TEXT NOT NULL DEFAULT '',
    branch TEXT,
    is_worktree INTEGER NOT NULL DEFAULT 0,
    main_repo TEXT,
    repository TEXT NOT NULL DEFAULT '',
    agent_metadata TEXT NOT NULL DEFAULT '',
    agent_driver_plugin_name TEXT NOT NULL DEFAULT '',
    agent_driver_run_id TEXT NOT NULL DEFAULT '',
    agent_driver_report_seq INTEGER NOT NULL DEFAULT 0,
    agent_driver_transcript_path TEXT NOT NULL DEFAULT '',
    turn_settled_at TEXT NOT NULL DEFAULT '',
    turn_opened_at TEXT NOT NULL DEFAULT '',
    turn_snoozed_until TEXT NOT NULL DEFAULT '',
    turn_agent_at TEXT NOT NULL DEFAULT '',
    turn_requested_at TEXT NOT NULL DEFAULT '',
    last_model_request_at TEXT,
    harness_title TEXT NOT NULL DEFAULT '',
    session_cost_json TEXT NOT NULL DEFAULT '',
    pending_switch TEXT NOT NULL DEFAULT ''
);

INSERT INTO session_runtime (
    session_id, state, state_since, state_updated_at, last_seen, branch, is_worktree, main_repo, repository,
    agent_metadata, agent_driver_plugin_name, agent_driver_run_id, agent_driver_report_seq, agent_driver_transcript_path,
    turn_settled_at, turn_opened_at, turn_snoozed_until, turn_agent_at, turn_requested_at,
    last_model_request_at, harness_title, session_cost_json
)
SELECT
    id, state, state_since, state_updated_at, last_seen, branch, is_worktree, main_repo, repository,
    agent_metadata, agent_driver_plugin_name, agent_driver_run_id, agent_driver_report_seq, agent_driver_transcript_path,
    turn_settled_at, turn_opened_at, turn_snoozed_until, turn_agent_at, turn_requested_at,
    last_model_request_at, harness_title, session_cost_json
FROM sessions;

DROP INDEX idx_sessions_repository;
CREATE INDEX idx_session_runtime_repository ON session_runtime(repository);

ALTER TABLE sessions DROP COLUMN state;
ALTER TABLE sessions DROP COLUMN state_since;
ALTER TABLE sessions DROP COLUMN state_updated_at;
ALTER TABLE sessions DROP COLUMN last_seen;
ALTER TABLE sessions DROP COLUMN branch;
ALTER TABLE sessions DROP COLUMN is_worktree;
ALTER TABLE sessions DROP COLUMN main_repo;
ALTER TABLE sessions DROP COLUMN repository;
ALTER TABLE sessions DROP COLUMN agent_metadata;
ALTER TABLE sessions DROP COLUMN agent_driver_plugin_name;
ALTER TABLE sessions DROP COLUMN agent_driver_run_id;
ALTER TABLE sessions DROP COLUMN agent_driver_report_seq;
ALTER TABLE sessions DROP COLUMN agent_driver_transcript_path;
ALTER TABLE sessions DROP COLUMN turn_settled_at;
ALTER TABLE sessions DROP COLUMN turn_opened_at;
ALTER TABLE sessions DROP COLUMN turn_snoozed_until;
ALTER TABLE sessions DROP COLUMN turn_agent_at;
ALTER TABLE sessions DROP COLUMN turn_requested_at;
ALTER TABLE sessions DROP COLUMN last_model_request_at;
ALTER TABLE sessions DROP COLUMN harness_title;
ALTER TABLE sessions DROP COLUMN session_cost_json;

CREATE VIEW sessions_with_runtime AS
SELECT
    s.*,
    COALESCE(r.state, 'launching') AS state,
    COALESCE(r.state_since, '') AS state_since,
    COALESCE(r.state_updated_at, '') AS state_updated_at,
    COALESCE(r.last_seen, '') AS last_seen,
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
    COALESCE(r.pending_switch, '') AS pending_switch
FROM sessions s
LEFT JOIN session_runtime r ON r.session_id = s.id;
