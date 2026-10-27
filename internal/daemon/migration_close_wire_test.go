package daemon_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/testworld"
)

func TestClosingAnImportedSessionUpdatesEveryMigrationClient(t *testing.T) {
	w := &world{World: prepareWorld(t)}
	db, err := store.OpenDBAtSchemaVersion(filepath.Join(w.Dir, "attn.db"), 153)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for i := 1; i <= 2; i++ {
		_, err := db.Exec(fmt.Sprintf(`
			INSERT INTO sessions (id, label, directory, state_since, state_updated_at, last_seen, workspace_id)
				VALUES ('agent-%[1]d', 'agent %[1]d', '/fixture', 'now', 'now', 'now', 'ws-%[1]d');
			INSERT INTO workspaces (id, title, directory, created_at, rank)
				VALUES ('ws-%[1]d', 'Workspace %[1]d', '/fixture/%[1]d', 'now', 'r%03[1]d');
			INSERT INTO workspace_layouts (workspace_id, active_pane_id, layout_json, updated_at)
				VALUES ('ws-%[1]d', 'pane-agent-%[1]d', '{"type":"pane","pane_id":"pane-agent-%[1]d"}', 'now');
			INSERT INTO workspace_layout_panes (workspace_id, pane_id, runtime_id, session_id, kind, title, status, error, created_at, updated_at)
				VALUES ('ws-%[1]d', 'pane-agent-%[1]d', 'agent-%[1]d', 'agent-%[1]d', 'agent', 'Agent', 'ready', '', 'now', 'now');
			INSERT INTO session_exit_screens (session_id, text, cols, rows, exit_code, exited_at)
				VALUES ('agent-%[1]d', 'previous process stopped', 80, 24, 1, '2026-01-01T00:00:00Z')`, i))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	w.start()
	first, second := w.App(), w.App()
	initial := testworld.Request(first, protocol.MigrationGetMessage{Cmd: protocol.CmdMigrationGet, RequestID: "before-close"},
		protocol.EventMigrationResult, func(r protocol.MigrationResultMessage) bool { return r.RequestID == "before-close" })
	if !initial.Success || initial.State == nil || len(initial.State.Groups) != 2 {
		t.Fatalf("migration before close = %+v, want two imported groups", initial)
	}
	if closed := closeFromApp(first, "agent-2"); !closed.Accepted {
		t.Fatalf("close an imported agent: %s", protocol.Deref(closed.Error))
	}
	for _, app := range []*testworld.Peer{first, second} {
		changed := testworld.Await(app, protocol.EventMigrationChanged, func(e protocol.MigrationChangedMessage) bool {
			return len(e.State.Groups) == 1
		})
		if changed.State.Groups[0].GroupID != "ws-1" {
			t.Fatalf("groups after close = %+v, want the other imported group", changed.State.Groups)
		}
	}
}
