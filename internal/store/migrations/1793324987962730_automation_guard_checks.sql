CREATE TABLE automation_guard_checks (
    definition_id INTEGER PRIMARY KEY REFERENCES automation_definitions(id),
    guard TEXT NOT NULL,
    checked_at TEXT NOT NULL,
    outcome TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    checks_since_run INTEGER NOT NULL DEFAULT 0,
    handled_output TEXT NOT NULL DEFAULT '',
    handled_run_id TEXT NOT NULL DEFAULT ''
);
