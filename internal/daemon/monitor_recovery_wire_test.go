package daemon_test

import (
	"github.com/victorarias/attn/internal/protocol"
	"strings"
	"testing"
)

func TestRecoveryUsesTheDriversSavedHandleRatherThanItsResumeCapability(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true, "resume": true})
	kept, run := spawnDriven(w, app, driver, w.Path("kept"))

	gone := "capability-only"
	if err := w.InjectSession(gone, "no driver handle", w.Path("gone"), "snipe"); err != nil {
		t.Fatal(err)
	}
	driver.mustReport("session.report_metadata", map[string]any{"session_id": run.SessionID, "run_id": run.RunID, "seq": 1, "metadata": map[string]string{"native_id": "resume-me"}, "resume_session_id": "resume-me"})
	driver.hangUp()
	w.restart()
	initial := w.App().Initial
	found := map[string]protocol.Session{}
	for _, s := range initial.Sessions {
		found[string(s.ID)] = s
	}
	if got, ok := found[kept]; !ok || got.State != protocol.SessionStateRecoverable {
		t.Fatalf("saved driver handle came back %+v present=%v", got, ok)
	}
	if got, ok := found[gone]; ok {
		t.Fatalf("driver capability with no conversation came back %+v", got)
	}
}

func TestRecoveryReapsOnlyTheUnresumablePaneAndKeepsItsMarkdownTile(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	registerSessions(t, w, w.Client(), "s1")
	markdownTileDock(t, w, app, "kept-notes", "markdown", w.Path("notes.md"))
	desktop := currentDesktop(t, w)
	w.restart()
	initial := w.App().Initial
	for _, s := range initial.Sessions {
		if s.ID == "s1" {
			t.Fatal("unresumable session survived")
		}
	}
	for _, d := range initial.Desktops {
		if d.ID == desktop.ID {
			ids := d.TreeJson
			if len(d.Panes) != 0 || !strings.Contains(ids, "kept-notes") {
				t.Fatalf("reaped desktop has panes %+v tiles %v", d.Panes, ids)
			}
			return
		}
	}
	t.Fatal("recovery dropped the desktop holding the markdown tile")
}
