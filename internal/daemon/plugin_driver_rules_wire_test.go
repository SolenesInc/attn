package daemon_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func statesShown(app *testworld.Peer, session string) []protocol.SessionState {
	var shown []protocol.SessionState
	for _, event := range app.Received() {
		sessions := slices.Clone(event.Sessions)
		if event.Session != nil {
			sessions = append(sessions, *event.Session)
		}
		for _, s := range sessions {
			if s.ID == session && (len(shown) == 0 || shown[len(shown)-1] != s.State) {
				shown = append(shown, s.State)
			}
		}
	}
	return shown
}

func listedState(t *testing.T, w *world, session string) protocol.Session {
	t.Helper()
	listed, err := w.Client().List("")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range listed.Sessions {
		if s.ID == session {
			return s
		}
	}
	t.Fatalf("session %s is not listed", session)
	return protocol.Session{}
}

func TestADriverSpeaksOnlyForTheRunItOwnsAndOnlyForward(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	owner := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	session, run := spawnDriven(w, app, owner, w.Path("shop"))

	stateIs := func(want protocol.SessionState, after string) {
		t.Helper()
		if got := listedState(t, w, session).State; got != want {
			t.Errorf("the session is %s after %s, want %s", got, after, want)
		}
	}
	if err := owner.state(run, 2, protocol.StateWorking); err != nil {
		t.Fatal(err)
	}
	if err := owner.state(run, 1, protocol.StateIdle); err != nil {
		t.Errorf("an out-of-order report was answered %v, want it quietly dropped", err)
	}
	stateIs(protocol.SessionStateWorking, "an out-of-order idle report")
	if err := owner.state(run, 4, protocol.StatePendingApproval); err != nil {
		t.Fatal(err)
	}
	owner.mustReport("session.report_stop", map[string]any{"session_id": session, "run_id": run.RunID, "seq": 3, "verdict": protocol.StateWaitingInput})
	stateIs(protocol.SessionStatePendingApproval, "an out-of-order stop")
	for _, refused := range []struct {
		name string
		run  string
		seq  uint64
		want string
	}{
		{name: "without a cursor", run: run.RunID, seq: 0, want: "seq must be greater than zero"},
		{name: "for another run", run: "run-stale", seq: 99, want: "does not own active run"},
	} {
		if err := owner.state(driverLaunch{SessionID: session, RunID: refused.run}, refused.seq, protocol.StateIdle); !errorSays(err, refused.want) {
			t.Errorf("a report %s was answered %v, want a refusal saying %q", refused.name, err, refused.want)
		}
	}
	stateIs(protocol.SessionStatePendingApproval, "refused reports")
	if err := owner.state(run, 5, protocol.StateWorking); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	if shown := statesShown(app, session); slices.Contains(shown, protocol.SessionStateIdle) || slices.Contains(shown, protocol.SessionStateWaitingInput) {
		t.Errorf("the app saw the session go %v, want stale and refused reports to show nothing", shown)
	}

	owner.leave(w)
	usurper := connectDriver(t, w, "usurper-plugin", "snipe", map[string]bool{"state_reporting": true})
	if err := usurper.state(run, 6, protocol.StateIdle); !errorSays(err, "does not own active run") {
		t.Errorf("another plugin registering the agent reported for its run and was answered %v, want an ownership refusal", err)
	}
	if got := listedState(t, w, session).State; got != protocol.SessionStateWorking {
		t.Errorf("the session is %s after the usurper's report, want working", got)
	}
}

func TestOnlyIfUnknownRestatesOnlyASessionAttnCouldNotTell(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	lost, lostRun := spawnDriven(w, app, driver, w.Path("lost"))
	settled, settledRun := spawnDriven(w, app, driver, w.Path("settled"))
	if err := driver.state(lostRun, 1, protocol.StateUnknown); err != nil {
		t.Fatal(err)
	}
	if err := driver.state(settledRun, 1, protocol.StateIdle); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(app, lost, func(s protocol.Session) bool { return s.State == protocol.SessionStateUnknown })
	before := testworld.AwaitSession(app, settled, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	restate := func(run driverLaunch, state string) {
		t.Helper()
		driver.mustReport("session.report_state", map[string]any{
			"session_id": run.SessionID, "run_id": run.RunID, "seq": 2, "state": state, "only_if_unknown": true,
		})
	}
	restate(lostRun, protocol.StateWorking)
	restate(settledRun, protocol.StateIdle)
	testworld.AwaitSession(app, lost, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	if after := listedState(t, w, settled); after.State != protocol.SessionStateIdle || after.StateSince != before.StateSince {
		t.Errorf("the settled session is %s since %s after an only-if-unknown report, want idle since %s", after.State, after.StateSince, before.StateSince)
	}
}

func TestAReconnectingDriverIsHandedItsOwnRunsAndTheAutoModeConfigItRuns(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	editAutoModeConfig(app, "host", protocol.AutoModeHostAddMessage{Cmd: protocol.CmdAutoModeHostAdd, Host: "crates.io", Decision: automode.HostAllow, RequestID: "host"})
	policy := uuid.NewString()
	editAutoModeConfig(app, policy, protocol.AutoModePolicySetMessage{Cmd: protocol.CmdAutoModePolicySet, RequestID: protocol.Ptr(policy), ApprovalPolicy: protocol.Ptr(automode.PolicyNever)})

	snipe := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
	rival := connectDriver(t, w, "rival-plugin", "rival", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "rival")
	if len(snipe.registered.ActiveRuns) != 0 || len(snipe.registered.AutoMode) != 0 {
		t.Errorf("a first registration without auto_mode was answered %+v, want no runs and no auto mode config", snipe.registered)
	}
	session, run := spawnDriven(w, app, snipe, w.Path("shop"))
	rivalSession := w.Spawn(app, "rival", w.Path("rival"))
	rival.launched()
	snipe.mustReport("session.report_metadata", map[string]any{
		"session_id": session, "run_id": run.RunID, "seq": 1, "metadata": json.RawMessage(`{"native_id":"abc"}`),
	})

	snipe.leave(w)
	back := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true, "auto_mode": true})
	runs := back.registered.ActiveRuns
	if len(runs) != 1 || runs[0].SessionID != session || runs[0].RunID != run.RunID || string(runs[0].Metadata) != `{"native_id":"abc"}` || runs[0].Seq != 1 {
		t.Errorf("the reconnected driver was handed %+v, want only its run of %s with its metadata at cursor 1 (not %s)", runs, session, rivalSession)
	}
	var config automode.Config
	if err := json.Unmarshal(back.registered.AutoMode, &config); err != nil {
		t.Fatalf("the auto_mode driver was handed config %s: %v", back.registered.AutoMode, err)
	}
	if config.ApprovalPolicy != automode.PolicyNever || !slices.Contains(config.Network.AllowedDomains, "crates.io") {
		t.Errorf("the auto_mode driver was handed policy %q and hosts %v, want the stored never and crates.io", config.ApprovalPolicy, config.Network.AllowedDomains)
	}
}

func closeSessionPane(app *testworld.Peer, session string) {
	app.T.Helper()
	if closed := closeFromApp(app, session); !closed.Accepted {
		app.T.Fatalf("closing %s: %s", session, protocol.Deref(closed.Error))
	}
}

func TestTheOwningDriverIsToldWhenEachOfItsRunsEnds(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	killed, killedRun := spawnDriven(w, app, driver, w.Path("killed"))
	paneClosed, paneClosedRun := spawnDriven(w, app, driver, w.Path("pane-closed"))
	finished, finishedRun := spawnDriven(w, app, driver, w.Path("finished"))

	for _, row := range []struct {
		name    string
		run     driverLaunch
		end     func()
		reasons []string
	}{
		{name: "killed", run: killedRun, reasons: []string{"killed", "exited"},
			end: func() { app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: killed}) }},
		{name: "pane closed", run: paneClosedRun, reasons: []string{"killed", "exited"},
			end: func() { closeSessionPane(app, paneClosed) }},
		{name: "exited on its own", run: finishedRun, reasons: []string{"exited"},
			end: func() { app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: finished, Data: "\x04"}) }},
	} {
		row.end()
		got := driver.closed()
		if got.SessionID != row.run.SessionID || got.RunID != row.run.RunID || !slices.Contains(row.reasons, got.Reason) {
			t.Errorf("%s: the driver was told %+v, want run %s of %s ended as one of %v", row.name, got, row.run.RunID, row.run.SessionID, row.reasons)
		}
		if row.name == "exited on its own" && (got.ExitCode == nil || *got.ExitCode != 0) {
			t.Errorf("%s: the driver was told exit code %v, want 0", row.name, protocol.Deref(got.ExitCode))
		}
		if err := driver.state(row.run, 9, protocol.StateIdle); err == nil {
			t.Errorf("%s: a report for the ended run was accepted", row.name)
		}
	}
}

func TestAnEndedRunIsReportedToTheDriverThatLaunchedItEvenAfterAnotherTookTheAgent(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	owner := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	session, run := spawnDriven(w, app, owner, w.Path("shop"))
	owner.leave(w)
	successor := connectDriver(t, w, "successor-plugin", "snipe", map[string]bool{"state_reporting": true})
	owner = dialDriver(t, w, "snipe-plugin")

	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: session})
	if got := owner.closed(); got.SessionID != session || got.RunID != run.RunID {
		t.Errorf("the launching plugin was told %+v, want the end of its run %s", got, run.RunID)
	}
	select {
	case got := <-successor.closes:
		t.Errorf("the plugin now holding the agent was told %+v about a run it never launched", got)
	default:
	}
}

func TestALaunchWhoseTerminalCannotStartClosesItsRun(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	driver.launchWith(w.Path("no-such-snipe"))
	refused := refuseSpawnLikeTheApp(w, app, scriptedAgent, w.Path("shop"))
	launch := driver.launched()
	if got := driver.closed(); got.SessionID != refused.ID || got.RunID != launch.RunID || got.Reason != "launch_failed" {
		t.Errorf("the driver was told %+v, want run %s of %s closed as launch_failed", got, launch.RunID, refused.ID)
	}
}

func TestAStopIsClassifiedOnlyForItsOwnRunAndAFailedVerdictIsUnknown(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app := w.App()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	session, run := spawnDriven(w, app, driver, w.Path("shop"))
	classify := func(runID, text string) (string, error) {
		var answer struct {
			Verdict string `json:"verdict"`
		}
		err := driver.call("attn.classify_stop", map[string]any{"session_id": session, "run_id": runID, "assistant_text": text}, &answer)
		return answer.Verdict, err
	}

	if _, err := classify("run-stale", "Should I proceed with the migration?"); !errorSays(err, "does not own active run") {
		t.Errorf("classifying another run's stop was answered %v, want an ownership refusal", err)
	}
	if _, err := classify(run.RunID, "   "); !errorSays(err, "assistant_text is required") {
		t.Errorf("classifying a blank stop was answered %v, want a refusal naming assistant_text", err)
	}

	verdict := make(chan string, 1)
	go func() {
		got, err := classify(run.RunID, "Should I proceed with the migration?")
		if err != nil {
			got = "error: " + err.Error()
		}
		verdict <- got
	}()
	w.HeadlessTask().Fail("the classifier crashed")
	if got := <-verdict; got != protocol.StateUnknown {
		t.Errorf("a stop whose classifier failed was answered %q, want unknown", got)
	}
}
