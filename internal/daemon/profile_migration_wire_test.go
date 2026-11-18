package daemon_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestAnUpgradedHomeOpensFinishedWithEveryWorkspaceOnItsOwnDesktopAndEveryLaunchItemOnItsOwn(t *testing.T) {
	const workspaces = 10
	w := &world{World: prepareWorld(t)}
	db, err := store.OpenDBAtSchemaVersion(filepath.Join(w.Dir, "attn.db"), 153)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= workspaces; i++ {
		if _, err := db.Exec(fmt.Sprintf(`
			INSERT INTO sessions (id, label, directory, state_since, state_updated_at, last_seen, workspace_id)
				VALUES ('agent-%[1]d', 'agent %[1]d', '/fixture', 'now', 'now', 'now', 'ws-%[1]d');
			INSERT INTO workspaces (id, title, directory, created_at, rank)
				VALUES ('ws-%[1]d', 'Workspace %[1]d', '/fixture/%[1]d', 'now', 'r%03[1]d');
			INSERT INTO workspace_layouts (workspace_id, active_pane_id, layout_json, updated_at)
				VALUES ('ws-%[1]d', 'pane-agent-%[1]d', '{"type":"pane","pane_id":"pane-agent-%[1]d"}', 'now');
			INSERT INTO workspace_layout_panes (workspace_id, pane_id, runtime_id, session_id, kind, title, status, error, created_at, updated_at)
				VALUES ('ws-%[1]d', 'pane-agent-%[1]d', 'agent-%[1]d', 'agent-%[1]d', 'agent', 'Agent', 'ready', '', 'now', 'now');
			INSERT INTO session_exit_screens (session_id, text, cols, rows, exit_code, exited_at)
				VALUES ('agent-%[1]d', 'previous process stopped', 80, 24, 1, '2026-01-01T00:00:00Z')`, i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO automation_definitions (id, name, enabled, revision, spec_json, created_at, updated_at)
		VALUES (1, 'Local check', 0, 1, '{"api_version":"attn.dev/automations/v1alpha1","id":1,"name":"Local check","trigger":{"type":"manual","repositories":{}},"prompt":"Check locally.","launch":{"driver":"claude"},"location":{"type":"directory","path":"/fixture","repository_sources":{"default":{"type":""}}}}', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	w.start()
	assertFinished := func() {
		t.Helper()
		app := w.App()
		if phase := protocol.Deref(app.Initial.MigrationPhase); phase != protocol.MigrationPhaseComplete {
			t.Fatalf("an upgraded home opened in migration phase %q, want complete", phase)
		}
		view := viewProfile(t, w, app.SelectedProfile())
		holders := map[string]string{}
		for i := 1; i <= workspaces; i++ {
			agent := fmt.Sprintf("agent-%d", i)
			desktop, _ := view.paneOf(t, agent)
			if other, shared := holders[desktop.ID]; shared || len(desktop.Panes) != 1 {
				t.Fatalf("%s shares desktop %s with %q: %+v", agent, desktop.ID, other, desktop.Panes)
			}
			holders[desktop.ID] = agent
		}
		for _, item := range []struct{ kind, id, label string }{
			{"automation", "1", "Local check (no ⌘ number)"},
			{"crew", "chief", "Chief (no ⌘ number)"},
		} {
			got := readLaunchSetting(app, item.kind, item.id)
			desktop, exists := view.desktops[protocol.Deref(got.Setting.DesktopID)]
			if !exists || holders[desktop.ID] != "" || protocol.Deref(got.Setting.Label) != item.label {
				t.Fatalf("%s %s launches on %+v (%+v), want its own desktop", item.kind, item.id, got, desktop)
			}
		}
	}
	assertFinished()
	w.restart()
	assertFinished()
}

func TestAHomeLeftInPlacementReviewWithACrewMembersOwnLaunchDesktopFinishesAtStartAndKeepsThatDesktop(t *testing.T) {
	w := &world{World: prepareWorld(t)}
	path := filepath.Join(w.Dir, "attn.db")
	db, err := store.OpenDBAtSchemaVersion(path, 153)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO sessions (id, label, directory, state_since, state_updated_at, last_seen, workspace_id)
			VALUES ('agent-1', 'agent 1', '/fixture', 'now', 'now', 'now', 'ws-1');
		INSERT INTO workspaces (id, title, directory, created_at, rank)
			VALUES ('ws-1', 'Workspace 1', '/fixture/1', 'now', 'r001');
		INSERT INTO workspace_layouts (workspace_id, active_pane_id, layout_json, updated_at)
			VALUES ('ws-1', 'pane-agent-1', '{"type":"pane","pane_id":"pane-agent-1"}', 'now');
		INSERT INTO workspace_layout_panes (workspace_id, pane_id, runtime_id, session_id, kind, title, status, error, created_at, updated_at)
			VALUES ('ws-1', 'pane-agent-1', 'agent-1', 'agent-1', 'agent', 'Agent', 'ready', '', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := store.NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	review, err := upgraded.ProfileMigration()
	if err != nil || !review.PlacementRequired() {
		t.Fatalf("the upgraded home is in %+v (%v), want placement review", review.State, err)
	}
	if err := upgraded.CreateCrewMember(store.CrewIdentity{Key: "trellis", ProfileID: review.Manifest.ProfileID, Name: "Trellis"}); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.SetLaunchDesktop("crew", "trellis", store.LaunchDesktopSetting{DesktopName: "Trellis"}); err != nil {
		t.Fatal(err)
	}
	saved, err := upgraded.LaunchDesktopItem("crew", "trellis")
	if err != nil || saved.DesktopID == "" {
		t.Fatalf("trellis saved %+v (%v), want its own desktop", saved, err)
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}

	w.start()
	assertFinished := func() {
		t.Helper()
		app := w.App()
		if phase := protocol.Deref(app.Initial.MigrationPhase); phase != protocol.MigrationPhaseComplete {
			t.Fatalf("the home opened in migration phase %q, want complete", phase)
		}
		got := readLaunchSetting(app, "crew", "trellis")
		if id := protocol.Deref(got.Setting.DesktopID); id != saved.DesktopID || protocol.Deref(got.Setting.Label) != "Trellis (no ⌘ number)" {
			t.Fatalf("trellis launches on %+v, want the desktop %s it saved", got.Setting, saved.DesktopID)
		}
	}
	assertFinished()
	w.restart()
	assertFinished()
}
