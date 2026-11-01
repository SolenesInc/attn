package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func reportTranscript(driver *driverPeer, session, runID, path string) error {
	return driver.report("session.report_transcript_path", map[string]any{"session_id": session, "run_id": runID, "path": path})
}

func TestAPluginTranscriptReportIsAcceptedOnlyForItsOwnRunWithAnAbsolutePath(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "attn-plug", "plug", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "plug")
	session, run := spawnDriven(w, app, driver, w.Path("shop"))
	path := w.Path("plug-session.jsonl")
	for _, refused := range []struct {
		name, session, run, path string
	}{
		{name: "for another run", session: session, run: "run-other", path: path},
		{name: "for an unknown session", session: "nobody", run: run.RunID, path: path},
		{name: "with a relative path", session: session, run: run.RunID, path: "sessions/s.jsonl"},
	} {
		if err := reportTranscript(driver, refused.session, refused.run, refused.path); err == nil {
			t.Errorf("a transcript report %s was accepted", refused.name)
		}
	}
	if err := reportTranscript(driver, run.SessionID, run.RunID, path); err != nil {
		t.Fatal(err)
	}
}

func TestATranscriptTheReloadingRunReportsBeforeItsLaunchCommitsIsAccepted(t *testing.T) {
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
	if err := reportTranscript(driver, reloading.SessionID, reloading.RunID, w.Path("plug-session.jsonl")); err != nil {
		t.Fatalf("the reloading run's transcript report was refused: %v", err)
	}
	driver.answer(reply, map[string]any{"argv": []string{"/bin/cat"}})
	if result := testworld.Await(app, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session }); !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	if closed := driver.closed(); closed.RunID != first.RunID {
		t.Fatalf("the reload closed run %s, want the replaced run %s", closed.RunID, first.RunID)
	}
}
