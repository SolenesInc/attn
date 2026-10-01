package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func migrationOrderError(registry []migration) error {
	for i := 1; i < len(registry); i++ {
		previous := registry[i-1].version
		current := registry[i].version
		switch {
		case current == previous:
			return fmt.Errorf("duplicate migration version %d at indexes %d and %d", current, i-1, i)
		case current < previous:
			return fmt.Errorf("migration version %d at index %d is out of order after version %d at index %d", current, i, previous, i-1)
		}
	}
	return nil
}

func TestMigrationVersionsStrictlyIncrease(t *testing.T) {
	if err := migrationOrderError(migrations); err != nil {
		t.Fatal(err)
	}
}

func TestMigrations_AppliedOnNewDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() error = %v", err)
	}
	defer db.Close()

	version, err := GetSchemaVersion(db)
	if err != nil {
		t.Fatalf("GetSchemaVersion() error = %v", err)
	}
	if version != latestSchemaVersion() {
		t.Errorf("schema version = %d, want %d", version, latestSchemaVersion())
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count)
	if err != nil {
		t.Fatalf("counting migrations error = %v", err)
	}
	if count != len(migrations) {
		t.Errorf("migration count = %d, want %d", count, len(migrations))
	}
}

func TestMigrations_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db1, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() first error = %v", err)
	}
	db1.Close()

	db2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() second error = %v", err)
	}
	defer db2.Close()

	var count int
	err = db2.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count)
	if err != nil {
		t.Fatalf("counting migrations error = %v", err)
	}
	if count != len(migrations) {
		t.Errorf("migration count after reopen = %d, want %d", count, len(migrations))
	}
}

func TestMigrations_MigratedColumnsExist(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() error = %v", err)
	}
	defer db.Close()

	migratedColumns := []struct {
		table  string
		column string
	}{
		{"prs", "host"},
		{"prs", "head_sha"},
		{"prs", "head_branch"},
		{"prs", "comment_count"},
		{"prs", "approved_by_me"},
		{"prs", "heat_state"},
		{"prs", "last_heat_activity_at"},
		{"pr_interactions", "last_seen_ci_status"},
		{"sessions", "branch"},
		{"sessions", "is_worktree"},
		{"sessions", "main_repo"},
		{"sessions", "agent"},
		{"sessions", "resume_session_id"},
		{"sessions", "transcript_path"},
		{"sessions", "endpoint_id"},
		{"sessions", "agent_metadata"},
		{"sessions", "agent_driver_plugin_name"},
		{"sessions", "agent_driver_run_id"},
		{"sessions", "agent_driver_report_seq"},
		{"chief_of_staff_dispatches", "structured_report_json"},
		{"sessions", "closed_intentionally_at"},
		{"automation_definitions", "spec_json"},
	}

	for _, tc := range migratedColumns {
		query := "SELECT " + tc.column + " FROM " + tc.table + " LIMIT 1"
		_, err := db.Exec(query)
		if err != nil {
			t.Errorf("Column %s.%s should exist after migrations: %v", tc.table, tc.column, err)
		}
	}
}

func latestSchemaVersion() int {
	max := 0
	for _, m := range migrations {
		if m.version > max {
			max = m.version
		}
	}
	return max
}

func TestMigration148PreservesPendingGardenMailboxReceiptsAndNamesItsBell(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration-148.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		DROP TABLE garden_seed_event_receipts;
		DROP TABLE garden_seed_event_sources;
		DROP TABLE garden_seed_artifact_observations;
		CREATE TABLE agent_mailbox_items(id TEXT PRIMARY KEY, recipient_session_id TEXT,kind TEXT,source_id TEXT,coalesce_key TEXT,hint TEXT,prompt TEXT,created_at TEXT,notified_at TEXT,read_at TEXT);
		INSERT INTO agent_mailbox_items
			(id, recipient_session_id, kind, source_id, coalesce_key, hint, prompt, created_at, notified_at, read_at)
		VALUES
			('pending', 'sess-a', 'garden_seed', 's-one', 's-one', 'unblocked', '', '2026-09-12T12:00:00Z', '2026-09-12T12:01:00Z', ''),
			('read', 'sess-a', 'garden_seed', 's-two', 's-two', 'lifecycle', '', '2026-09-12T12:00:00Z', '2026-09-12T12:01:00Z', '2026-09-12T12:02:00Z')
	`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigration148(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var hint, bell, notified, read string
	if err := db.QueryRow(`SELECT hint,bell_name,notified_at,read_at FROM agent_mailbox_items WHERE id='pending'`).Scan(&hint, &bell, &notified, &read); err != nil {
		t.Fatal(err)
	}
	if hint != "unblocked" || bell != "seed activity" || notified != "2026-09-12T12:01:00Z" || read != "" {
		t.Fatalf("pending row after migration = hint=%q bell=%q notified=%q read=%q", hint, bell, notified, read)
	}
	if err := db.QueryRow(`SELECT hint,bell_name,notified_at,read_at FROM agent_mailbox_items WHERE id='read'`).Scan(&hint, &bell, &notified, &read); err != nil {
		t.Fatal(err)
	}
	if hint != "lifecycle" || bell != "" || notified != "2026-09-12T12:01:00Z" || read != "2026-09-12T12:02:00Z" {
		t.Fatalf("read row after migration = hint=%q bell=%q notified=%q read=%q", hint, bell, notified, read)
	}
	for _, table := range []string{"garden_seed_event_receipts", "garden_seed_event_sources", "garden_seed_artifact_observations"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatalf("%s missing after migration: %v", table, err)
		}
	}
}

func TestMigration73RepairsAutomationInstanceMigration70Collision(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-73-collision.db")
	db, err := OpenDBAtSchemaVersion(dbPath, 70)
	if err != nil {
		t.Fatalf("OpenDB() setup error = %v", err)
	}

	if _, err := db.Exec(`
		DROP TABLE delegation_operations;
		DELETE FROM schema_migrations WHERE version >= 71;
	`); err != nil {
		db.Close()
		t.Fatalf("seed migration 70 collision: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seeded db: %v", err)
	}

	migrated, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB() repair migration 70 collision: %v", err)
	}
	defer migrated.Close()

	for _, column := range []string{"worktree_token", "chief_session_id"} {
		var count int
		if err := migrated.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('delegation_operations') WHERE name = ?`,
			column,
		).Scan(&count); err != nil {
			t.Fatalf("query delegation_operations.%s: %v", column, err)
		}
		if count != 1 {
			t.Fatalf("delegation_operations.%s count = %d, want 1", column, count)
		}
	}
}

func TestMigration143AddsDelegationHandoverSnapshot(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-143.db")
	db, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"handover_seed_rev", "handover_tender_session", "handover_tender_member"} {
		if _, err := db.Exec(`ALTER TABLE delegation_operations DROP COLUMN ` + column); err != nil {
			db.Close()
			t.Fatalf("drop delegation_operations.%s: %v", column, err)
		}
	}

	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version >= 143`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	migrated, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	for _, column := range []string{"handover_seed_rev", "handover_tender_session", "handover_tender_member"} {
		var count int
		if err := migrated.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('delegation_operations') WHERE name = ?`, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("delegation_operations.%s count = %d, err = %v", column, count, err)
		}
	}
}

func TestMigration144AddsDelegationParentSnapshot(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-144.db")
	db, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE delegation_operations DROP COLUMN parent_seed_id`); err != nil {
		db.Close()
		t.Fatal(err)
	}

	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version >= 144`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	migrated, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	var count int
	if err := migrated.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('delegation_operations') WHERE name = 'parent_seed_id'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("delegation_operations.parent_seed_id count = %d, err = %v", count, err)
	}
}

func TestMigration75DefaultsExistingRowsToEmptySpecYAML(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-75.db")
	db, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB() setup error = %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO automation_definitions (id, name, enabled, revision, spec_json, created_at, updated_at, deleted_at) VALUES (?, ?, 1, 1, ?, ?, ?, '')`,
		"legacy-def", "Legacy", `{"id":"legacy-def","name":"Legacy"}`, now, now,
	); err != nil {
		db.Close()
		t.Fatalf("seed legacy row: %v", err)
	}

	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version >= 75`); err != nil {
		db.Close()
		t.Fatalf("roll back to pre-migration-75 schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seeded db: %v", err)
	}

	migrated, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB() migration 75/76 = %v", err)
	}
	defer migrated.Close()

	var defCount int
	if err := migrated.QueryRow(`SELECT COUNT(*) FROM automation_definitions`).Scan(&defCount); err != nil {
		t.Fatalf("count automation_definitions: %v", err)
	}
	if defCount != 0 {
		t.Fatalf("automation_definitions row count = %d, want 0 (migration 76 clears every row)", defCount)
	}
	var specYAMLColumns int
	if err := migrated.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('automation_definitions') WHERE name = 'spec_yaml'`,
	).Scan(&specYAMLColumns); err != nil {
		t.Fatalf("query automation_definitions.spec_yaml: %v", err)
	}
	if specYAMLColumns != 0 {
		t.Fatalf("automation_definitions.spec_yaml columns = %d, want 0", specYAMLColumns)
	}
}

func TestMigration76ClearsAutomationStateAndDropsSpecYAML(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-76.db")
	db, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB() setup error = %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)

	if _, err := db.Exec(`
		ALTER TABLE automation_definitions ADD COLUMN spec_yaml TEXT NOT NULL DEFAULT '';
		ALTER TABLE automation_review_request_edges ADD COLUMN accepted_cycle INTEGER NOT NULL DEFAULT 0;
		DELETE FROM schema_migrations WHERE version >= 76;
	`); err != nil {
		db.Close()
		t.Fatalf("roll back to pre-migration-76 schema: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO automation_definitions (id, name, enabled, revision, spec_json, spec_yaml, created_at, updated_at, deleted_at) VALUES (?, ?, 1, 1, ?, ?, ?, ?, '')`,
		"legacy-def", "Legacy", `{"id":"legacy-def","name":"Legacy"}`, "id: legacy-def\nname: Legacy\n", now, now,
	); err != nil {
		db.Close()
		t.Fatalf("seed automation_definitions: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO automation_occurrences (id, definition_id, provider, occurrence_key, observed_at, payload_json, created_at) VALUES (?, ?, 'manual', 'request-1', ?, '{}', ?)`,
		"occ-1", "legacy-def", now, now,
	); err != nil {
		db.Close()
		t.Fatalf("seed automation_occurrences: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO automation_runs (id, definition_id, occurrence_id, definition_revision, snapshot_json, state, ticket_id, session_id, created_at, updated_at) VALUES (?, ?, ?, 1, '{}', 'delivered', ?, 'session-1', ?, ?)`,
		"run-1", "legacy-def", "occ-1", "legacy-ticket", now, now,
	); err != nil {
		db.Close()
		t.Fatalf("seed automation_runs: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO automation_ticket_occurrence_events (run_id, ticket_id, created_at) VALUES (?, ?, ?)`,
		"run-1", "legacy-ticket", now,
	); err != nil {
		db.Close()
		t.Fatalf("seed automation_ticket_occurrence_events: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO automation_continuity_bindings (definition_id, continuity_key, ticket_id, session_id, created_at, updated_at) VALUES (?, 'fresh', ?, 'session-1', ?, ?)`,
		"legacy-def", "legacy-ticket", now, now,
	); err != nil {
		db.Close()
		t.Fatalf("seed automation_continuity_bindings: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO automation_review_request_edges (definition_id, subject_key, host, active, cycle, accepted_cycle, last_observed_at, updated_at) VALUES (?, 'subject-1', 'github.com', 1, 1, 1, ?, ?)`,
		"legacy-def", now, now,
	); err != nil {
		db.Close()
		t.Fatalf("seed automation_review_request_edges: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO automation_provider_cursors (definition_id, provider, scope, observed_at) VALUES (?, 'github', 'repo', ?)`,
		"legacy-def", now,
	); err != nil {
		db.Close()
		t.Fatalf("seed automation_provider_cursors: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO tickets (id, title, status, automation_run_id, created_at, updated_at) VALUES ('legacy-ticket', 'Legacy ticket', 'working', ?, ?, ?)`,
		"run-1", now, now,
	); err != nil {
		db.Close()
		t.Fatalf("seed tickets: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seeded db: %v", err)
	}

	migrated, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB() migration 76 = %v", err)
	}
	defer migrated.Close()

	for _, table := range []string{
		"automation_runs", "automation_occurrences",
		"automation_continuity_bindings", "automation_review_request_edges", "automation_provider_cursors",
	} {
		var count int
		if err := migrated.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s`, table)).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s row count = %d, want 0 (migration 76 must wipe automation state)", table, count)
		}
	}

	var specYAMLColumns int
	if err := migrated.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('automation_definitions') WHERE name = 'spec_yaml'`,
	).Scan(&specYAMLColumns); err != nil {
		t.Fatalf("query automation_definitions.spec_yaml: %v", err)
	}
	if specYAMLColumns != 0 {
		t.Fatalf("automation_definitions.spec_yaml columns = %d, want 0 (migration 76 drops the column)", specYAMLColumns)
	}

	var defCount int
	if err := migrated.QueryRow(`SELECT COUNT(*) FROM automation_definitions`).Scan(&defCount); err != nil {
		t.Fatalf("count automation_definitions: %v", err)
	}
	if defCount != 0 {
		t.Fatalf("automation_definitions row count = %d, want 0 (recreated empty)", defCount)
	}

}

func TestMigration77ClearsRunsBindingsAndEdges(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-77.db")
	db, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB() setup error = %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)

	if _, err := db.Exec(`
		DROP TABLE automation_runs;
		CREATE TABLE automation_runs (
			id TEXT PRIMARY KEY,
			definition_id TEXT NOT NULL,
			occurrence_id TEXT NOT NULL,
			definition_revision INTEGER NOT NULL,
			snapshot_json TEXT NOT NULL,
			state TEXT NOT NULL,
			last_error TEXT NOT NULL DEFAULT '',
			ticket_id TEXT,
			session_id TEXT,
			workspace_id TEXT,
			pane_id TEXT,
			resolved_location_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			delivered_at TEXT,
			FOREIGN KEY (definition_id) REFERENCES automation_definitions(id),
			FOREIGN KEY (occurrence_id) REFERENCES automation_occurrences(id)
		);
		CREATE INDEX idx_automation_runs_definition_created ON automation_runs(definition_id, created_at);

		DROP TABLE automation_continuity_bindings;
		CREATE TABLE automation_continuity_bindings (
			definition_id TEXT NOT NULL,
			continuity_key TEXT NOT NULL,
			ticket_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			pane_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (definition_id, continuity_key),
			FOREIGN KEY (definition_id) REFERENCES automation_definitions(id)
		);

		DROP TABLE automation_review_request_edges;
		CREATE TABLE automation_review_request_edges (
			definition_id TEXT NOT NULL,
			subject_key TEXT NOT NULL,
			host TEXT NOT NULL,
			active INTEGER NOT NULL,
			cycle INTEGER NOT NULL,
			accepted_cycle INTEGER NOT NULL DEFAULT 0,
			last_observed_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (definition_id, subject_key, host),
			FOREIGN KEY (definition_id) REFERENCES automation_definitions(id)
		);
		CREATE INDEX idx_automation_review_edges_host ON automation_review_request_edges(host, active);

		DELETE FROM schema_migrations WHERE version >= 77;
	`); err != nil {
		db.Close()
		t.Fatalf("roll back to pre-migration-77 schema: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO automation_definitions (id, name, enabled, revision, spec_json, created_at, updated_at, deleted_at) VALUES (?, ?, 1, 1, ?, ?, ?, '')`,
		"legacy-def", "Legacy", `{"id":"legacy-def","name":"Legacy"}`, now, now,
	); err != nil {
		db.Close()
		t.Fatalf("seed automation_definitions: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO automation_occurrences (id, definition_id, provider, occurrence_key, observed_at, payload_json, created_at) VALUES (?, ?, 'manual', 'request-1', ?, '{}', ?)`,
		"occ-1", "legacy-def", now, now,
	); err != nil {
		db.Close()
		t.Fatalf("seed automation_occurrences: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO automation_runs (id, definition_id, occurrence_id, definition_revision, snapshot_json, state, ticket_id, session_id, workspace_id, pane_id, created_at, updated_at) VALUES (?, ?, ?, 1, '{}', 'delivered', ?, 'session-1', 'workspace-1', 'pane-1', ?, ?)`,
		"run-1", "legacy-def", "occ-1", "legacy-ticket", now, now,
	); err != nil {
		db.Close()
		t.Fatalf("seed automation_runs: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO automation_ticket_occurrence_events (run_id, ticket_id, created_at) VALUES (?, ?, ?)`,
		"run-1", "legacy-ticket", now,
	); err != nil {
		db.Close()
		t.Fatalf("seed automation_ticket_occurrence_events: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO automation_continuity_bindings (definition_id, continuity_key, ticket_id, session_id, workspace_id, pane_id, created_at, updated_at) VALUES (?, 'fresh', ?, 'session-1', 'workspace-1', 'pane-1', ?, ?)`,
		"legacy-def", "legacy-ticket", now, now,
	); err != nil {
		db.Close()
		t.Fatalf("seed automation_continuity_bindings: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO automation_review_request_edges (definition_id, subject_key, host, active, cycle, accepted_cycle, last_observed_at, updated_at) VALUES (?, 'subject-1', 'github.com', 1, 1, 1, ?, ?)`,
		"legacy-def", now, now,
	); err != nil {
		db.Close()
		t.Fatalf("seed automation_review_request_edges: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO automation_provider_cursors (definition_id, provider, scope, observed_at) VALUES (?, 'github', 'repo', ?)`,
		"legacy-def", now,
	); err != nil {
		db.Close()
		t.Fatalf("seed automation_provider_cursors: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO tickets (id, title, status, automation_run_id, created_at, updated_at) VALUES ('legacy-ticket', 'Legacy ticket', 'working', ?, ?, ?)`,
		"run-1", now, now,
	); err != nil {
		db.Close()
		t.Fatalf("seed tickets: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seeded db: %v", err)
	}

	migrated, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB() migration 77 = %v", err)
	}
	defer migrated.Close()

	for _, table := range []string{
		"automation_runs", "automation_occurrences",
		"automation_continuity_bindings", "automation_review_request_edges", "automation_provider_cursors",
	} {
		var count int
		if err := migrated.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s`, table)).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s row count = %d, want 0 (migration 77 must wipe automation state)", table, count)
		}
	}

	var acceptedCycleColumns int
	if err := migrated.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('automation_review_request_edges') WHERE name = 'accepted_cycle'`,
	).Scan(&acceptedCycleColumns); err != nil {
		t.Fatalf("query automation_review_request_edges.accepted_cycle: %v", err)
	}
	if acceptedCycleColumns != 0 {
		t.Fatalf("automation_review_request_edges.accepted_cycle columns = %d, want 0 (migration 77 drops it)", acceptedCycleColumns)
	}

	for _, col := range []string{"cancel_reason", "attempts"} {
		var n int
		if err := migrated.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('automation_runs') WHERE name = ?`, col,
		).Scan(&n); err != nil {
			t.Fatalf("query automation_runs.%s: %v", col, err)
		}
		if n != 1 {
			t.Fatalf("automation_runs.%s columns = %d, want 1", col, n)
		}
	}

	for _, col := range []string{"id", "status", "released_reason", "released_at"} {
		var n int
		if err := migrated.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('automation_continuity_bindings') WHERE name = ?`, col,
		).Scan(&n); err != nil {
			t.Fatalf("query automation_continuity_bindings.%s: %v", col, err)
		}
		if n != 1 {
			t.Fatalf("automation_continuity_bindings.%s columns = %d, want 1", col, n)
		}
	}

	var activeIndexCount int
	if err := migrated.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_automation_bindings_active'`,
	).Scan(&activeIndexCount); err != nil {
		t.Fatalf("query idx_automation_bindings_active: %v", err)
	}
	if activeIndexCount != 1 {
		t.Fatalf("idx_automation_bindings_active count = %d, want 1", activeIndexCount)
	}

	if _, err := migrated.Exec(
		`INSERT INTO automation_continuity_bindings (id, definition_id, continuity_key, ticket_id, session_id, status, created_at, updated_at) VALUES ('b1', 'legacy-def', 'fresh', 't1', 's1', 'active', ?, ?)`,
		now, now,
	); err != nil {
		t.Fatalf("seed first active binding: %v", err)
	}
	if _, err := migrated.Exec(
		`INSERT INTO automation_continuity_bindings (id, definition_id, continuity_key, ticket_id, session_id, status, created_at, updated_at) VALUES ('b2', 'legacy-def', 'fresh', 't2', 's2', 'active', ?, ?)`,
		now, now,
	); err == nil {
		t.Fatal("expected unique-active index to reject a second active binding for the same definition+continuity_key")
	}
}

func TestMigration131_RepairsPartialAgentDriverCursorSchemas(t *testing.T) {
	columns := []struct {
		name    string
		dropSQL string
	}{
		{"plugin-name", `ALTER TABLE sessions DROP COLUMN agent_driver_plugin_name`},
		{"run-id", `ALTER TABLE sessions DROP COLUMN agent_driver_run_id`},
		{"report-seq", `ALTER TABLE sessions DROP COLUMN agent_driver_report_seq`},
	}

	for missing := 0; missing < 1<<len(columns); missing++ {
		name := "complete-schema"
		var missingNames []string
		for i, column := range columns {
			if missing&(1<<i) != 0 {
				missingNames = append(missingNames, column.name)
			}
		}
		if len(missingNames) > 0 {
			name = "missing-" + strings.Join(missingNames, "-and-")
		}
		t.Run(name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "migration-131.db")
			db, err := openDBAtVersion(dbPath, 161)
			if err != nil {
				t.Fatalf("OpenDB setup: %v", err)
			}
			if _, err := db.Exec(`
				INSERT INTO sessions (
					id, label, directory, state, state_since, state_updated_at, last_seen,
					agent_driver_plugin_name, agent_driver_run_id, agent_driver_report_seq
				) VALUES (
					'partial-driver', 'Partial driver', '/tmp/partial-driver', 'idle',
					'2026-05-25T20:22:10Z', '2026-05-25T20:22:10Z', '2026-05-25T20:22:10Z',
					'example-plugin', 'run-39', 7
				)
			`); err != nil {
				db.Close()
				t.Fatalf("seed session: %v", err)
			}
			for i, column := range columns {
				if missing&(1<<i) == 0 {
					continue
				}
				if _, err := db.Exec(column.dropSQL); err != nil {
					db.Close()
					t.Fatalf("drop %s column: %v", column.name, err)
				}
			}

			if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version >= 131`); err != nil {
				db.Close()
				t.Fatalf("rewind migration 131: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close partial database: %v", err)
			}

			migrated, err := openDBAtVersion(dbPath, 161)
			if err != nil {
				t.Fatalf("OpenDB migrate: %v", err)
			}
			defer migrated.Close()

			want := AgentDriverReportCursor{PluginName: "example-plugin", RunID: "run-39", Seq: 7}
			if missing&1 != 0 {
				want.PluginName = ""
			}
			if missing&2 != 0 {
				want.RunID = ""
			}
			if missing&4 != 0 {
				want.Seq = 0
			}
			var got AgentDriverReportCursor
			if err := migrated.QueryRow(`SELECT agent_driver_plugin_name, agent_driver_run_id, agent_driver_report_seq
				FROM sessions WHERE id = 'partial-driver'`).Scan(&got.PluginName, &got.RunID, &got.Seq); err != nil {
				t.Fatalf("read repaired cursor: %v", err)
			}
			if got != want {
				t.Fatalf("repaired cursor = %+v, want %+v", got, want)
			}

			store := &Store{db: migrated}
			teardown, err := store.PrepareSessionTeardown("partial-driver", time.Date(2026, 9, 2, 12, 30, 0, 0, time.UTC))
			if err != nil {
				t.Fatalf("PrepareSessionTeardown after repair: %v", err)
			}
			if teardown != want {
				t.Fatalf("teardown cursor = %+v, want %+v", teardown, want)
			}

			var applied int
			if err := migrated.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 131`).Scan(&applied); err != nil {
				t.Fatalf("read migration 131: %v", err)
			}
			if applied != 1 {
				t.Fatalf("migration 131 count = %d, want 1", applied)
			}
		})
	}
}

func TestMigration79_ConvertsRecoverableFlagToState(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-79.db")
	db, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB setup: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO sessions (id, label, directory, state, state_since, state_updated_at, last_seen)
		VALUES ('recoverable-session', 'Recoverable', '/tmp/recoverable', 'idle', '2026-07-24T00:00:00Z', '2026-07-24T00:00:00Z', '2026-07-24T00:00:00Z');
		ALTER TABLE sessions ADD COLUMN recoverable INTEGER NOT NULL DEFAULT 0;
		UPDATE sessions SET recoverable = 1 WHERE id = 'recoverable-session';
		DELETE FROM schema_migrations WHERE version >= 79;
	`); err != nil {
		t.Fatalf("seed pre-79 database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close pre-79 database: %v", err)
	}

	migrated, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB migrate: %v", err)
	}
	defer migrated.Close()

	var state string
	if err := migrated.QueryRow(`SELECT state FROM sessions WHERE id = 'recoverable-session'`).Scan(&state); err != nil {
		t.Fatalf("read migrated session: %v", err)
	}
	if state != "recoverable" {
		t.Fatalf("state = %q, want recoverable", state)
	}

	if _, err := migrated.Exec(`SELECT recoverable FROM sessions LIMIT 1`); err == nil {
		t.Fatal("recoverable column still exists after migration")
	}
	if _, err := migrated.Exec(`DELETE FROM schema_migrations WHERE version >= 79`); err != nil {
		t.Fatalf("rewind migration 79 after column drop: %v", err)
	}
	if err := migrated.Close(); err != nil {
		t.Fatalf("close migrated database: %v", err)
	}
	migrated, err = openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("rerun migration 79 without column: %v", err)
	}
	if err := migrated.Close(); err != nil {
		t.Fatalf("close reopened database: %v", err)
	}
}

func TestMigration20_IdempotentWhenHostColumnAlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB() setup error = %v", err)
	}
	db.Close()

	raw, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB() reopen setup error = %v", err)
	}

	if _, err := raw.Exec("DELETE FROM schema_migrations WHERE version >= 20"); err != nil {
		raw.Close()
		t.Fatalf("DELETE migration >=20 markers error = %v", err)
	}
	if _, err := raw.Exec("ALTER TABLE prs ADD COLUMN host TEXT NOT NULL DEFAULT 'github.com'"); err != nil {
		_ = err
	}
	raw.Close()

	db2, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB() should handle existing prs.host in migration 20, got error = %v", err)
	}
	defer db2.Close()

	version, err := GetSchemaVersion(db2)
	if err != nil {
		t.Fatalf("GetSchemaVersion() error = %v", err)
	}
	if version != 161 {
		t.Fatalf("schema version = %d, want %d", version, 161)
	}

	var idxName string
	err = db2.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_prs_host_repo_number'
	`).Scan(&idxName)
	if err != nil {
		t.Fatalf("expected migration 20 index to exist: %v", err)
	}
	if idxName != "idx_prs_host_repo_number" {
		t.Fatalf("index name = %q, want idx_prs_host_repo_number", idxName)
	}

	var count int
	if err := db2.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 20").Scan(&count); err != nil {
		t.Fatalf("count migration 20 row error = %v", err)
	}
	if count != 1 {
		t.Fatalf("migration 20 marker count = %d, want 1", count)
	}

}

func TestMigration21_IdempotentWhenAgentColumnAlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB() setup error = %v", err)
	}
	db.Close()

	raw, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB() reopen setup error = %v", err)
	}

	if _, err := raw.Exec("DELETE FROM schema_migrations WHERE version >= 21"); err != nil {
		raw.Close()
		t.Fatalf("DELETE migration >=21 markers error = %v", err)
	}
	if _, err := raw.Exec("ALTER TABLE sessions ADD COLUMN agent TEXT NOT NULL DEFAULT 'codex'"); err != nil {
		_ = err
	}
	raw.Close()

	db2, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB() should handle existing sessions.agent in migration 21, got error = %v", err)
	}
	defer db2.Close()

	version, err := GetSchemaVersion(db2)
	if err != nil {
		t.Fatalf("GetSchemaVersion() error = %v", err)
	}
	if version != 161 {
		t.Fatalf("schema version = %d, want %d", version, 161)
	}

	var count int
	if err := db2.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 21").Scan(&count); err != nil {
		t.Fatalf("count migration 21 row error = %v", err)
	}
	if count != 1 {
		t.Fatalf("migration 21 marker count = %d, want 1", count)
	}
}

func TestMigration31_IdempotentWhenEndpointIDColumnAlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB() setup error = %v", err)
	}
	db.Close()

	raw, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB() reopen setup error = %v", err)
	}

	if _, err := raw.Exec("DELETE FROM schema_migrations WHERE version >= 31"); err != nil {
		raw.Close()
		t.Fatalf("DELETE migration >=31 markers error = %v", err)
	}
	if _, err := raw.Exec("ALTER TABLE sessions ADD COLUMN endpoint_id TEXT"); err != nil {
		_ = err
	}
	raw.Close()

	db2, err := openDBAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("OpenDB() should handle existing sessions.endpoint_id in migration 31, got error = %v", err)
	}
	defer db2.Close()

	version, err := GetSchemaVersion(db2)
	if err != nil {
		t.Fatalf("GetSchemaVersion() error = %v", err)
	}
	if version != 161 {
		t.Fatalf("schema version = %d, want %d", version, 161)
	}

	var count int
	if err := db2.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 31").Scan(&count); err != nil {
		t.Fatalf("count migration 31 row error = %v", err)
	}
	if count != 1 {
		t.Fatalf("migration 31 marker count = %d, want 1", count)
	}
}

func TestMigration134DropsTheWorkspaceContextAndKeeperState(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := newStoreAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	statements := []string{
		`CREATE TABLE workspace_contexts (workspace_id TEXT PRIMARY KEY, content TEXT NOT NULL)`,
		`CREATE TABLE workspace_keeper_compact_backups (workspace_id TEXT PRIMARY KEY, source_content TEXT NOT NULL)`,
		`INSERT INTO workspace_contexts (workspace_id, content) VALUES ('workspace-1', '# Workspace Context')`,
		`INSERT INTO settings (key, value) VALUES ('workspace_keeper_compact', '{"agent":"claude","model":"haiku"}'), ('notebook.summarize_session.enabled', 'false'), ('notebook.cron.frequency', '0 3 * * *'), ('notebook.root', '/tmp/notebook')`,
		`INSERT INTO jobs (id, kind, unique_key, state, scheduled_at, created_at, updated_at) VALUES ('job-1', 'summarize_session', 'session-1', 'queued', '2026-09-05T00:00:00Z', '2026-09-05T00:00:00Z', '2026-09-05T00:00:00Z'), ('job-2', 'compact_context', 'workspace-1', 'queued', '2026-09-05T00:00:00Z', '2026-09-05T00:00:00Z', '2026-09-05T00:00:00Z'), ('job-3', 'session_title', 'session-1', 'queued', '2026-09-05T00:00:00Z', '2026-09-05T00:00:00Z', '2026-09-05T00:00:00Z')`,
		`INSERT INTO tasks (id, kind, subject, state, attempts, next_attempt_at, last_error, meta_json, requeued, created_at, updated_at) VALUES ('summarize_session:s-2', 'summarize_session', 's-2', 'queued', 1, '2026-09-05T00:00:00Z', '', '{}', 0, '2026-09-05T00:00:00Z', '2026-09-05T00:00:00Z'), ('reconcile:t-2', 'reconcile', 't-2', 'queued', 1, '2026-09-05T00:00:00Z', '', '{}', 0, '2026-09-05T00:00:00Z', '2026-09-05T00:00:00Z')`,
		`DELETE FROM schema_migrations WHERE version >= 134`,
	}
	for _, stmt := range statements {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	if err := migrateDBThrough(s.db, dbPath, 161); err != nil {
		t.Fatalf("migrateDB error: %v", err)
	}

	for _, table := range []string{"workspace_contexts", "workspace_keeper_compact_backups"} {
		if tableExistsForTest(t, s.db, table) {
			t.Fatalf("%s still present after migration 134", table)
		}
	}
	var settings int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key IN ('workspace_keeper_compact', 'notebook.summarize_session.enabled', 'notebook.cron.frequency')`).Scan(&settings); err != nil {
		t.Fatalf("count keeper settings: %v", err)
	}
	if settings != 0 {
		t.Fatalf("keeper settings remaining = %d, want 0", settings)
	}
	var notebookRoot string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'notebook.root'`).Scan(&notebookRoot); err != nil {
		t.Fatalf("read notebook.root: %v", err)
	}
	if notebookRoot != "/tmp/notebook" {
		t.Fatalf("notebook.root = %q, want it untouched", notebookRoot)
	}
	var kinds []string
	rows, err := s.db.Query(`SELECT kind FROM jobs ORDER BY id`)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatalf("scan job: %v", err)
		}
		kinds = append(kinds, kind)
	}
	if len(kinds) != 1 || kinds[0] != "session_title" {
		t.Fatalf("jobs after migration = %v, want only session_title", kinds)
	}

}

func TestMigration53AddsClosedStateColumnIdempotently(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := newStoreAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	hasClosedState := func() bool {
		rows, err := s.db.Query(`PRAGMA table_info(chief_of_staff_dispatches)`)
		if err != nil {
			t.Fatalf("table_info: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				cid     int
				name    string
				colType string
				notNull int
				dflt    sql.NullString
				pk      int
			)
			if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
				t.Fatalf("scan table_info: %v", err)
			}
			if name == "closed_state" {
				return true
			}
		}
		return false
	}

	if !hasClosedState() {
		t.Fatal("closed_state column missing after migrations")
	}

	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 53`); err != nil {
		t.Fatalf("unrecord migration 53: %v", err)
	}
	if err := migrateDBThrough(s.db, dbPath, 161); err != nil {
		t.Fatalf("re-run migrateDB after unrecording 53: %v", err)
	}
	if !hasClosedState() {
		t.Fatal("closed_state column missing after idempotent re-run")
	}
}

func tableExistsForTest(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var got string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("tableExists(%s): %v", name, err)
	}
	return got == name
}

func TestMigration121BackfillsTheRequestClockAndIsRewindSafe(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := newStoreAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	observed := "2026-08-23T10:15:00Z"
	s.Add(&protocol.Session{
		ID: "legacy-session", State: protocol.SessionStateWaitingInput,
		StateSince: observed, StateUpdatedAt: observed, LastSeen: observed,
	})
	if _, err := s.db.Exec(`UPDATE sessions SET last_model_request_at = NULL WHERE id = 'legacy-session'`); err != nil {
		t.Fatalf("clear request clock: %v", err)
	}

	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 121`); err != nil {
		t.Fatalf("rewind through migration 121: %v", err)
	}

	if err := migrateDBThrough(s.db, dbPath, 161); err != nil {
		t.Fatalf("first migrateDB: %v", err)
	}
	if err := migrateDBThrough(s.db, dbPath, 161); err != nil {
		t.Fatalf("second migrateDB: %v", err)
	}
	var requestAt string
	if err := s.db.QueryRow(`SELECT last_model_request_at FROM sessions WHERE id = 'legacy-session'`).Scan(&requestAt); err != nil {
		t.Fatalf("read request clock: %v", err)
	}
	if requestAt != observed {
		t.Fatalf("last_model_request_at = %q, want state observation %q", requestAt, observed)
	}
}

func TestMigration123AddsTranscriptPathAndIsRewindSafe(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := newStoreAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	s.Add(&protocol.Session{ID: "legacy-session", Agent: protocol.SessionAgentCodex})
	s.SetResumeSessionID("legacy-session", "native-legacy")

	if _, err := s.db.Exec(`
		ALTER TABLE sessions DROP COLUMN transcript_path;
		DELETE FROM schema_migrations WHERE version >= 123;
	`); err != nil {
		t.Fatalf("rewind migration 123: %v", err)
	}

	if err := migrateDBThrough(s.db, dbPath, 161); err != nil {
		t.Fatalf("first migrateDB: %v", err)
	}

	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 123`); err != nil {
		t.Fatalf("unrecord migration 123: %v", err)
	}
	if err := migrateDBThrough(s.db, dbPath, 161); err != nil {
		t.Fatalf("second migrateDB: %v", err)
	}

	if got := s.GetSessionConversation("legacy-session"); got != (SessionConversation{NativeID: "native-legacy"}) {
		t.Fatalf("migrated binding = %+v, want native ID with an empty path", got)
	}
}

func TestMigration145AdoptsGardenDispatchForAutomationContinuity(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := newStoreAtVersion(dbPath, 161)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, "", now)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:one", "singleton", def.Revision, `{}`, `{}`, now, AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", SeedID: "s-old000", SessionID: "session-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.Exec(`INSERT INTO tickets(id,title,status,assignee,automation_run_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, "legacy-ticket", "Review", "working", run.SessionID, run.ID, formatStoredTime(now), formatStoredTime(now)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DefineDocumentCollection(garden.DispatchesSchema(), now); err != nil {
		t.Fatal(err)
	}
	dispatches, found, err := s.DocumentCollection(garden.Namespace, garden.CollectionDispatches)
	if err != nil || !found {
		t.Fatalf("load dispatch collection: found=%v err=%v", found, err)
	}
	if _, err := s.PutDocument(*dispatches, run.SessionID, []byte(`{"session_id":"session-1","crown":"s-live01"}`), now, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.Exec(`
		UPDATE automation_runs SET seed_id='',ticket_id='legacy-ticket' WHERE id='run-1';
		UPDATE automation_continuity_bindings SET seed_id='',origin_run_id='',ticket_id='legacy-ticket' WHERE definition_id='review';
		DELETE FROM schema_migrations WHERE version>=145;
	`); err != nil {
		t.Fatalf("rewind migration 145: %v", err)
	}

	if err := migrateDBThrough(s.db, dbPath, 161); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}
	migratedRun, err := s.GetAutomationRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := s.GetActiveAutomationContinuityBinding(def.ID, "singleton")
	if err != nil {
		t.Fatal(err)
	}
	if migratedRun == nil || binding == nil || migratedRun.SeedID != "s-live01" || binding.SeedID != "s-live01" || binding.OriginRunID != run.ID {
		t.Fatalf("migrated run=%#v binding=%#v", migratedRun, binding)
	}
}
