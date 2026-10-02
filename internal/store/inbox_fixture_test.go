package store

import (
	"database/sql"
	"testing"
)

// Older migration fixtures rewind version records; restore the tables removed by migration 162 as well.
func restorePreInboxFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(preInboxTicketSchema); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='inbox_items')").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		return
	}
	_, err := db.Exec(`
 CREATE TABLE agent_mailbox_items (
 id TEXT PRIMARY KEY,recipient_session_id TEXT NOT NULL,kind TEXT NOT NULL,source_id TEXT NOT NULL DEFAULT '',
 coalesce_key TEXT NOT NULL DEFAULT '',hint TEXT NOT NULL DEFAULT '',prompt TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,notified_at TEXT NOT NULL DEFAULT '',read_at TEXT NOT NULL DEFAULT '',
 bell_name TEXT NOT NULL DEFAULT '',CHECK(read_at='' OR notified_at!=''));
 INSERT INTO agent_mailbox_items SELECT id,substr(address,9),kind,source_id,coalesce_key,hint,text,created_at,notified_at,read_at,bell_name FROM inbox_items;
 DROP TABLE inbox_items;
 DROP TABLE inbox_delivery;
 ALTER TABLE presentations DROP COLUMN address;
 CREATE INDEX idx_agent_mailbox_recipient_unread ON agent_mailbox_items(recipient_session_id,created_at,id) WHERE read_at='';
 CREATE INDEX idx_agent_mailbox_source ON agent_mailbox_items(kind,source_id,recipient_session_id);
 CREATE UNIQUE INDEX idx_agent_mailbox_unread_coalesce ON agent_mailbox_items(recipient_session_id,kind,coalesce_key) WHERE coalesce_key!='' AND read_at='';
 ALTER TABLE pull_request_watches RENAME TO addressed_watches_fixture;
 CREATE TABLE pull_request_watches (
 session_id TEXT NOT NULL,pr_id TEXT NOT NULL,mode TEXT NOT NULL,reviewer TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,cursor_json TEXT NOT NULL DEFAULT '{}',last_success_at TEXT NOT NULL DEFAULT '',
 last_error TEXT NOT NULL DEFAULT '',feedback_error TEXT NOT NULL DEFAULT '',outage_active INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(session_id,pr_id));
 INSERT INTO pull_request_watches SELECT session_id,pr_id,mode,reviewer,created_at,cursor_json,last_success_at,last_error,feedback_error,outage_active FROM addressed_watches_fixture;
 DROP TABLE addressed_watches_fixture;
 CREATE INDEX idx_pull_request_watches_pr ON pull_request_watches(pr_id,session_id);
 DELETE FROM schema_migrations WHERE version=162;
 `)
	if err != nil {
		t.Fatal(err)
	}
}

const preInboxTicketSchema = `
CREATE TABLE IF NOT EXISTS automation_ticket_occurrence_events (
			run_id TEXT PRIMARY KEY,
			ticket_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(run_id) REFERENCES automation_runs(id),
			FOREIGN KEY(ticket_id) REFERENCES tickets(id)
		);
CREATE TABLE IF NOT EXISTS legacy_ticket_recovery_items (
			fingerprint              TEXT PRIMARY KEY,
			run_version              INTEGER NOT NULL,
			source_kind              TEXT NOT NULL,
			source_key               TEXT NOT NULL,
			ticket_id                TEXT NOT NULL DEFAULT '',
			recovered_local_identity TEXT NOT NULL DEFAULT '',
			result                   TEXT NOT NULL,
			detail                   TEXT NOT NULL DEFAULT '',
			created_at               TEXT NOT NULL
		);
CREATE TABLE IF NOT EXISTS legacy_ticket_recovery_runs (
			version                 INTEGER PRIMARY KEY,
			state                   TEXT NOT NULL,
			inventory_json          TEXT NOT NULL,
			counts_json             TEXT NOT NULL DEFAULT '{}',
			warning_notification_id TEXT NOT NULL DEFAULT '',
			started_at              TEXT NOT NULL,
			recovery_at             TEXT NOT NULL,
			finished_at             TEXT NOT NULL DEFAULT '',
			terminal_error          TEXT NOT NULL DEFAULT ''
		);
CREATE TABLE IF NOT EXISTS legacy_ticket_recovery_sources (
			run_version INTEGER NOT NULL,
			path        TEXT NOT NULL,
			family      TEXT NOT NULL,
			size        INTEGER NOT NULL,
			mod_time_ns INTEGER NOT NULL,
			sha256      TEXT NOT NULL,
			state       TEXT NOT NULL DEFAULT 'pending',
			detail      TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (run_version, path)
		);
CREATE TABLE IF NOT EXISTS legacy_ticket_seed_links (
			ticket_id               TEXT PRIMARY KEY,
			seed_id                 TEXT NOT NULL UNIQUE,
			source_kind             TEXT NOT NULL,
			evidence_fingerprint    TEXT NOT NULL,
			original_terminal_state TEXT NOT NULL,
			created_at              TEXT NOT NULL
		);
CREATE TABLE IF NOT EXISTS ticket_activity (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ticket_id   TEXT NOT NULL,
    kind        TEXT NOT NULL,
    author      TEXT NOT NULL DEFAULT '',
    from_status TEXT NOT NULL DEFAULT '',
    to_status   TEXT NOT NULL DEFAULT '',
    comment     TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS ticket_attachments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ticket_id   TEXT NOT NULL,
    filename    TEXT NOT NULL,
    path        TEXT NOT NULL DEFAULT '',
    note        TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS ticket_delivery_attention (
		observer_key TEXT PRIMARY KEY,
		last_attention_at TEXT NOT NULL
	, delivered_through_seq INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS ticket_event_cursors (
    identity   TEXT NOT NULL,
    ticket_id  TEXT NOT NULL,
    cursor     INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (identity, ticket_id),
    FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS ticket_events (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    ticket_id   TEXT NOT NULL,
    kind        TEXT NOT NULL,
    author      TEXT NOT NULL DEFAULT '',
    from_status TEXT NOT NULL DEFAULT '',
    to_status   TEXT NOT NULL DEFAULT '',
    comment     TEXT NOT NULL DEFAULT '',
    detail      TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL, author_role TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS ticket_role_owners (
			role TEXT NOT NULL,
			ticket_id TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (role, ticket_id),
			FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
		);
CREATE TABLE IF NOT EXISTS ticket_subscriptions (
    identity   TEXT NOT NULL,
    ticket_id  TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (identity, ticket_id),
    FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS tickets (
    id            TEXT PRIMARY KEY,
    title         TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL,
    assignee      TEXT NOT NULL DEFAULT '',
    cwd           TEXT NOT NULL DEFAULT '',
    last_agent_id TEXT NOT NULL DEFAULT '',
    project_id    TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    closed_at     TEXT NOT NULL DEFAULT '',
    archived_at   TEXT NOT NULL DEFAULT ''
, resume_session_id TEXT NOT NULL DEFAULT '', reconciled_at TEXT NOT NULL DEFAULT '', automation_run_id TEXT);
`
