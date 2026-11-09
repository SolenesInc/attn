CREATE TABLE update_runs (
    id TEXT PRIMARY KEY,
    target_build TEXT NOT NULL DEFAULT '',
    install_kind TEXT NOT NULL,
    phase TEXT NOT NULL,
    outcome TEXT NOT NULL DEFAULT '',
    log_path TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    finished_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE update_requests (
    id TEXT PRIMARY KEY,
    requester TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL,
    run_id TEXT NOT NULL DEFAULT '',
    notification_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE INDEX idx_update_requests_state ON update_requests(state);
CREATE INDEX idx_update_requests_run ON update_requests(run_id);
