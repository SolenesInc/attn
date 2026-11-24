package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func reportTitle(driver *driverPeer, session, runID, title string) error {
	return driver.report("session.report_title", map[string]any{"session_id": session, "run_id": runID, "title": title})
}

func TestAPluginTitleReportUpdatesTheSessionLabelAndRespectsUserRenames(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "attn-plug", "plug", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "plug")
	session, run := spawnDriven(w, app, driver, w.Path("shop"))

	for _, refused := range []struct {
		name, session, run, title string
	}{
		{name: "for another run", session: session, run: "run-other", title: "Fix login"},
		{name: "for an unknown session", session: "nobody", run: run.RunID, title: "Fix login"},
		{name: "with an empty title", session: session, run: run.RunID, title: "   \n\t  "},
	} {
		if err := reportTitle(driver, refused.session, refused.run, refused.title); err == nil {
			t.Errorf("a title report %s was accepted", refused.name)
		}
	}

	if err := reportTitle(driver, run.SessionID, run.RunID, "  Refactor\tsession\ntitles  "); err != nil {
		t.Fatal(err)
	}
	awaitLabel(app, session, "Refactor session titles")

	if err := reportTitle(driver, run.SessionID, run.RunID, "Renamed inside harness"); err != nil {
		t.Fatal(err)
	}
	awaitLabel(app, session, "Renamed inside harness")

	renameFromApp(app, protocol.RenameSessionMessage{Cmd: protocol.CmdRenameSession, SessionID: protocol.SessionID(session), Label: "Pinned by user"}, session)
	awaitLabel(app, session, "Pinned by user")

	if err := reportTitle(driver, run.SessionID, run.RunID, "Later harness title"); err != nil {
		t.Fatal(err)
	}
	if label := w.sessionLabel(session); label != "Pinned by user" {
		t.Fatalf("session label = %q after harness title report, want explicit user rename preserved", label)
	}
}

func TestATitleTheReloadingRunReportsBeforeItsLaunchCommitsIsApplied(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "attn-plug", "plug", map[string]bool{"state_reporting": true, "resume": true})
	awaitDriverAvailable(app, "plug")
	session, first := spawnDriven(w, app, driver, w.Path("shop"))
	driver.mu.Lock()
	driver.holdLaunch = true
	driver.mu.Unlock()
	app.Send(protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 80, Rows: 24})
	var reloading driverLaunch
	reply := driver.asked("driver.resume", &reloading)
	driver.launched()
	if err := reportTitle(driver, reloading.SessionID, reloading.RunID, "Restored session title"); err != nil {
		t.Fatalf("the reloading run's title report was refused: %v", err)
	}
	driver.answer(reply, map[string]any{"argv": []string{"/bin/cat"}})
	if result := testworld.Await(app, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session }); !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	if closed := driver.closed(); closed.RunID != first.RunID {
		t.Fatalf("the reload closed run %s, want the replaced run %s", closed.RunID, first.RunID)
	}
	awaitLabel(app, session, "Restored session title")
}
