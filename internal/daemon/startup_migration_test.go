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

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func writeLegacyDatabase(t *testing.T, path string, agents int, schemaVersion int, extra ...string) {
	t.Helper()
	db, err := store.OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	statements := []string{`DELETE FROM desktop_panes; DELETE FROM desktops; DELETE FROM setups; DELETE FROM setup_migration; UPDATE sessions SET setup_id = ''`}
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
	statements = append(statements, fmt.Sprintf(`DELETE FROM schema_migrations WHERE version > %d`, schemaVersion))
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
	_, err := store.OpenCurrent(path)
	var behind *store.SchemaBehindError
	if errors.As(err, &behind) {
		return behind.Current
	}
	if err != nil {
		t.Fatalf("OpenCurrent: %v", err)
	}
	return store.LatestSchemaVersion()
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

func TestAnEnrolledOutpostRefusesToStartBeforeTheLockAndTheDatabase(t *testing.T) {
	d, dir := productionDaemon(t)
	if _, err := enrollment.EnsureDaemonID(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := enrollment.Enroll(dir, "d-0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}

	err := d.Start()
	var outpost *enrollment.OutpostError
	if !errors.As(err, &outpost) {
		t.Fatalf("Start = %v, want an outpost refusal", err)
	}
	for _, name := range []string{"attn.pid", "attn.db"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s exists after the outpost refusal (%v)", name, err)
		}
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
	view, err := next.store.SetupMigration()
	if err != nil || !view.PlacementRequired() || len(view.Live) != 2 {
		t.Fatalf("migration after the repaired start = %+v, %v; want two groups waiting for placement", view.State, err)
	}
}

func newMigratingTestDaemon(t *testing.T, agents int) *setupsTestDaemon {
	t.Helper()
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	w := &setupsTestDaemon{t: t, dbPath: filepath.Join(t.TempDir(), "attn.db")}
	writeLegacyDatabase(t, w.dbPath, agents, store.SetupConversionSchemaVersion-1)
	w.start()
	return w
}

func (w *setupsTestDaemon) migrate(client *wsClient, command map[string]any) protocol.MigrationResultMessage {
	w.t.Helper()
	if _, ok := command["request_id"]; !ok {
		command["request_id"] = "req-" + command["cmd"].(string)
	}
	data, err := json.Marshal(command)
	if err != nil {
		w.t.Fatal(err)
	}
	w.d.handleClientMessage(client, data)
	var result *protocol.MigrationResultMessage
	for _, payload := range drainClientPayloads(w.t, client) {
		if result != nil || eventName(w.t, payload) != protocol.EventMigrationResult {
			client.send <- outboundMessage{kind: messageKindText, payload: payload}
			continue
		}
		result = &protocol.MigrationResultMessage{}
		decodeInto(w.t, payload, result)
	}
	if result == nil {
		w.t.Fatalf("%s was not answered with migration_result", command["cmd"])
	}
	if result.RequestID != command["request_id"] {
		w.t.Fatalf("result answers %q, want %q", result.RequestID, command["request_id"])
	}
	return *result
}

func (w *setupsTestDaemon) mustMigrate(client *wsClient, command map[string]any) protocol.MigrationState {
	w.t.Helper()
	result := w.migrate(client, command)
	if !result.Success || result.State == nil {
		w.t.Fatalf("%s failed: %s", command["cmd"], protocol.Deref(result.Error))
	}
	return *result.State
}

func migrationBroadcasts(t *testing.T, client *wsClient) []protocol.MigrationState {
	t.Helper()
	var states []protocol.MigrationState
	for _, payload := range drainClientPayloads(t, client) {
		if eventName(t, payload) != protocol.EventMigrationChanged {
			continue
		}
		var changed protocol.MigrationChangedMessage
		decodeInto(t, payload, &changed)
		states = append(states, changed.State)
	}
	return states
}

func TestTwoClientsShareOneMigrationDraftAndEitherMayFinish(t *testing.T) {
	w := newMigratingTestDaemon(t, 3)
	a, initialA := w.connect("")
	b, _ := w.connect("")
	if initialA.MigrationPhase == nil || *initialA.MigrationPhase != protocol.MigrationPhasePlacementRequired {
		t.Fatalf("initial_state migration phase = %v, want placement_required", initialA.MigrationPhase)
	}
	state := w.mustMigrate(a, map[string]any{"cmd": protocol.CmdMigrationGet})
	if len(state.Groups) != 3 || len(state.Desktops) != 9 || state.Groups[0].Confirmed {
		t.Fatalf("draft = %+v, want three unconfirmed groups over nine slots", state)
	}
	target := state.Groups[0].SourceDesktopID
	moved := w.mustMigrate(a, map[string]any{
		"cmd": protocol.CmdMigrationMove, "expected_revision": state.Revision,
		"group_id": "ws-2", "target_key": target, "edge": "right", "share": 0.3,
	})
	if seen := migrationBroadcasts(t, b); len(seen) != 1 || seen[0].Revision != moved.Revision {
		t.Fatalf("the second client saw %+v, want the move at revision %d", seen, moved.Revision)
	}

	stale := w.migrate(b, map[string]any{"cmd": protocol.CmdMigrationKeep, "expected_revision": state.Revision, "group_ids": []string{"ws-1"}})
	if stale.Success || stale.ErrorCode == nil || *stale.ErrorCode != protocol.SetupErrorCodeStaleRevision {
		t.Fatalf("a stale keep = %+v, want stale_revision", stale)
	}
	early := w.migrate(b, map[string]any{"cmd": protocol.CmdMigrationFinish, "expected_revision": moved.Revision})
	if early.Success || early.ErrorCode == nil || *early.ErrorCode != protocol.SetupErrorCodeInvalid {
		t.Fatalf("finishing with unconfirmed groups = %+v, want invalid", early)
	}
	kept := w.mustMigrate(b, map[string]any{"cmd": protocol.CmdMigrationKeep, "expected_revision": moved.Revision, "group_ids": []string{"ws-1", "ws-3"}})
	drainClientPayloads(t, a)

	done := w.mustMigrate(b, map[string]any{"cmd": protocol.CmdMigrationFinish, "expected_revision": kept.Revision})
	if done.Phase != protocol.MigrationPhaseComplete {
		t.Fatalf("finish returned phase %s", done.Phase)
	}
	var sawComplete, sawArrangement bool
	for _, payload := range drainClientPayloads(t, a) {
		switch eventName(t, payload) {
		case protocol.EventMigrationChanged:
			var changed protocol.MigrationChangedMessage
			decodeInto(t, payload, &changed)
			sawComplete = changed.State.Phase == protocol.MigrationPhaseComplete
		case protocol.EventSetupArrangementChanged:
			var changed protocol.SetupArrangementChangedMessage
			decodeInto(t, payload, &changed)
			for _, desktop := range changed.Desktops {
				if desktop.ID == target && len(desktop.Panes) == 2 {
					sawArrangement = true
				}
			}
		}
	}
	if !sawComplete || !sawArrangement {
		t.Fatalf("the first client saw complete=%v merged desktop=%v", sawComplete, sawArrangement)
	}
	again := w.mustMigrate(a, map[string]any{"cmd": protocol.CmdMigrationFinish, "expected_revision": moved.Revision})
	if again.Phase != protocol.MigrationPhaseComplete {
		t.Fatalf("a retried finish = %+v, want the committed completion", again)
	}
}

func TestClosingAnImportedAgentRetiresItsGroupForEveryClient(t *testing.T) {
	w := newMigratingTestDaemon(t, 2)
	client, _ := w.connect("")
	w.d.closeSession("agent-2", store.SessionClose{})
	seen := migrationBroadcasts(t, client)
	if len(seen) == 0 {
		t.Fatal("closing an imported agent sent no migration_changed")
	}
	last := seen[len(seen)-1]
	if len(last.Groups) != 1 || last.Groups[0].GroupID != "ws-1" {
		t.Fatalf("groups after the close = %+v, want only ws-1", last.Groups)
	}
}
