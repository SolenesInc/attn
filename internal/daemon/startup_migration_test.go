package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

func writeLegacyDatabase(t *testing.T, path string, agents int, schemaVersion int, extra ...string) {
	t.Helper()
	db, err := store.OpenDBAtSchemaVersion(path, schemaVersion)
	if err != nil {
		t.Fatalf("OpenDBAtSchemaVersion: %v", err)
	}
	defer db.Close()
	var statements []string
	for i := 1; i <= agents; i++ {
		statements = append(statements, fmt.Sprintf(`
			INSERT INTO sessions (id, label, directory, state_since, state_updated_at, last_seen, workspace_id)
				VALUES ('agent-%[1]d', 'agent %[1]d', '/fixture', 'now', 'now', 'now', 'ws-%[1]d');
			INSERT INTO workspaces (id, title, directory, created_at, rank) VALUES ('ws-%[1]d', 'Workspace %[1]d', '/fixture/%[1]d', 'now', 'r%03[1]d');
			INSERT INTO workspace_layouts (workspace_id, active_pane_id, layout_json, updated_at)
				VALUES ('ws-%[1]d', 'pane-agent-%[1]d', '{"type":"pane","pane_id":"pane-agent-%[1]d"}', 'now');
			INSERT INTO workspace_layout_panes (workspace_id, pane_id, runtime_id, session_id, kind, title, status, error, created_at, updated_at)
				VALUES ('ws-%[1]d', 'pane-agent-%[1]d', 'agent-%[1]d', 'agent-%[1]d', 'agent', 'Agent', 'ready', '', 'now', 'now')`, i))
	}
	statements = append(statements, extra...)
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("legacy fixture: %v", err)
		}
	}
}

func productionDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	dir := shortTempDir(t)
	t.Setenv("ATTN_DATA_DIR", dir)
	t.Setenv("ATTN_WS_PORT", "0")
	return New(filepath.Join(dir, "attn.sock")), dir
}

func schemaVersionOf(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	version, err := store.GetSchemaVersion(db)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func TestADaemonThatFindsTheLockHeldExitsWithoutTouchingTheDatabase(t *testing.T) {
	d, dir := productionDaemon(t)
	holder, err := os.OpenFile(filepath.Join(dir, "attn.pid"), os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}

	if err := d.Start(); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Start = %v, want ErrAlreadyRunning", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "attn.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the database exists after a refused start (%v)", err)
	}
}

func TestAFailedConversionStopsStartupAndLeavesAMarkerUntilAGoodStart(t *testing.T) {
	d, dir := productionDaemon(t)
	dbPath := filepath.Join(dir, "attn.db")
	writeLegacyDatabase(t, dbPath, 2, 149,
		`INSERT INTO workspaces (id, title, directory, created_at, rank) VALUES ('ws-broken', 'Broken', '/fixture', 'now', 'z')`,
		`INSERT INTO workspace_layouts (workspace_id, active_pane_id, layout_json, updated_at) VALUES ('ws-broken', '', '{"type":"pane"', 'now')`)

	err := d.Start()
	if err == nil || !strings.Contains(err.Error(), MigrationFailureFileName) {
		t.Fatalf("Start = %v, want a failure pointing at the marker", err)
	}
	raw, err := os.ReadFile(MigrationFailurePath(dir))
	if err != nil {
		t.Fatalf("marker: %v", err)
	}
	var marker MigrationFailure
	if err := json.Unmarshal(raw, &marker); err != nil {
		t.Fatalf("marker %s: %v", raw, err)
	}
	if marker.DataDir != dir || marker.DatabasePath != dbPath || marker.DaemonLogPath != filepath.Join(dir, "daemon.log") ||
		marker.SchemaVersionFrom != 149 || marker.SchemaVersionTo != store.LatestSchemaVersion() ||
		!strings.Contains(marker.Error, `legacy workspace ws-broken ("Broken")`) || marker.BinaryVersion == "" {
		t.Fatalf("marker = %+v", marker)
	}
	if _, err := time.Parse(time.RFC3339, marker.FailedAt); err != nil {
		t.Fatalf("failed_at %q: %v", marker.FailedAt, err)
	}
	if _, err := os.Stat(marker.BackupPath); err != nil {
		t.Fatalf("backup %q: %v", marker.BackupPath, err)
	}
	if version := schemaVersionOf(t, dbPath); version != 149 {
		t.Fatalf("schema version after the failure = %d, want 149", version)
	}

	repair, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repair.Exec(`DELETE FROM workspace_layouts WHERE workspace_id = 'ws-broken'`); err != nil {
		t.Fatal(err)
	}
	repair.Close()
	next := New(filepath.Join(dir, "attn.sock"))
	next.dataRoot = dir
	if err := next.openStore(); err != nil {
		t.Fatalf("a start after the repair: %v", err)
	}
	defer next.store.Close()
	if _, err := os.Stat(MigrationFailurePath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the marker survived a good start (%v)", err)
	}
	view, err := next.store.ProfileMigration()
	if err != nil || !view.PlacementRequired() || len(view.Live) != 2 {
		t.Fatalf("migration after the repaired start = %+v, %v; want two groups waiting for placement", view.State, err)
	}
}
