package daemon_test

import (
	"encoding/json"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func exitDriven(app *testworld.Peer, driver *driverPeer, session string) {
	app.T.Helper()
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: session, Data: "\x04"})
	if closed := driver.closed(); closed.SessionID != session || closed.Reason != "exited" {
		app.T.Fatalf("the driver was told %+v, want %s to have exited", closed, session)
	}
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })
}

func relaunchDriven(w *world, driver *driverPeer, session, cwd string) driverLaunch {
	w.T.Helper()
	w.Spawn(w.App(), fakeagent.Harness(driver.agent), cwd, func(m *protocol.SpawnSessionMessage) { m.ID = session })
	return driver.launched()
}

func hasNoMetadata(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

func TestADriverThatResumesIsHandedTheConversationItWasNamedOrLastReported(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"resume": true, "state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	cwd := w.Path("shop")
	session, named := spawnDriven(w, app, driver, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ResumeSessionID = protocol.Ptr("snipe-conv-7")
	})
	if named.Method != "driver.resume" || named.ResumeSessionID != "snipe-conv-7" || !hasNoMetadata(named.Metadata) {
		t.Errorf("a spawn naming a conversation asked the driver %s for %q with metadata %s, want driver.resume for snipe-conv-7 and none", named.Method, named.ResumeSessionID, named.Metadata)
	}

	metadata := func(seq uint64, native string) {
		t.Helper()
		driver.mustReport("session.report_metadata", map[string]any{
			"session_id": session, "run_id": named.RunID, "seq": seq,
			"metadata": map[string]string{"snipe_session_id": native}, "resume_session_id": native,
		})
	}
	metadata(1, "native-id")
	metadata(1, "older")
	exitDriven(app, driver, session)

	relaunch := relaunchDriven(w, driver, session, cwd)
	if relaunch.Method != "driver.resume" || relaunch.ResumeSessionID != "native-id" || string(relaunch.Metadata) != `{"snipe_session_id":"native-id"}` {
		t.Errorf("the relaunch asked the driver %s for %q with metadata %s, want driver.resume of native-id with the metadata reported first", relaunch.Method, relaunch.ResumeSessionID, relaunch.Metadata)
	}
}

func TestADriverWithoutResumeRelaunchesFreshWhateverConversationItReported(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	cwd := w.Path("shop")
	session, run := spawnDriven(w, app, driver, cwd)
	driver.mustReport("session.report_metadata", map[string]any{
		"session_id": session, "run_id": run.RunID, "seq": 1,
		"metadata": map[string]string{"snipe_session_id": "native-id"}, "resume_session_id": "native-id",
	})
	exitDriven(app, driver, session)

	if relaunch := relaunchDriven(w, driver, session, cwd); relaunch.Method != "driver.spawn" || relaunch.ResumeSessionID != "" {
		t.Errorf("the relaunch asked the driver %s for %q, want a fresh driver.spawn", relaunch.Method, relaunch.ResumeSessionID)
	}
}
