package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func reportTitle(driver *driverPeer, session, runID, title string) error {
	return driver.report("session.report_title", map[string]any{"session_id": session, "run_id": runID, "title": title})
}

func reportPrompt(driver *driverPeer, session, runID, text string) error {
	return driver.report("session.report_prompt", map[string]any{"session_id": session, "run_id": runID, "text": text})
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

func TestAPluginPromptReportTitlesTheSessionAndLaterTitleOverridesUnlessUserPinned(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app := w.App()
	driver := connectDriver(t, w, "attn-plug", "plug", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "plug")
	session, run := spawnDriven(w, app, driver, w.Path("shop"))

	for _, refused := range []struct {
		name, session, run, text string
	}{
		{name: "for another run", session: session, run: "run-other", text: "fix the login flow"},
		{name: "for an unknown session", session: "nobody", run: run.RunID, text: "fix the login flow"},
		{name: "with empty text", session: session, run: run.RunID, text: "  \n\t  "},
	} {
		if err := reportPrompt(driver, refused.session, refused.run, refused.text); err == nil {
			t.Errorf("a prompt report %s was accepted", refused.name)
		}
	}

	app.TypeLine(session, "the login form rejects valid passwords")
	if err := reportPrompt(driver, run.SessionID, run.RunID, "the login form rejects valid passwords"); err != nil {
		t.Fatal(err)
	}
	task := w.HeadlessTask()
	if !strings.Contains(task.Prompt, "the login form rejects valid passwords") {
		t.Fatalf("the title task asked %q, want the reported prompt", task.Prompt)
	}
	task.Answer("Fix login flow")
	awaitLabel(app, session, "Fix login flow")

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
		t.Fatalf("session label = %q after later harness title, want explicit user rename preserved", label)
	}
}

func TestANotificationRingDoesNotTitleAPluginSessionThatReportsItsPrompt(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	sender := w.Spawn(app, fakeagent.Claude, w.Path("sender"))
	w.Launched(sender)
	driver := connectDriver(t, w, "attn-plug", "plug", map[string]bool{"state_reporting": true, "message_delivery": true, "attributed_input": true})
	awaitDriverAvailable(app, "plug")
	session, run := spawnDriven(w, app, driver, w.Path("shop"))
	driver.mustReport("session.report_metadata", map[string]any{"session_id": run.SessionID, "run_id": run.RunID, "seq": 1, "metadata": map[string]string{"native_id": "conv-1"}, "resume_session_id": "conv-1"})
	if err := driver.state(run, 2, protocol.StateIdle); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	reported := make(chan error, 1)
	go func() {
		var input deliveredMessage
		call := driver.asked("driver.deliver_message", &input)
		err := reportPrompt(driver, run.SessionID, run.RunID, input.Text)
		driver.answer(call, map[string]bool{"ok": true})
		reported <- err
	}()
	sendAgentMessage(t, cli, sender, session, "check the staging deploy")
	if err := <-reported; err != nil {
		t.Fatal(err)
	}
	if err := driver.state(run, 3, protocol.StateWorking); err != nil {
		t.Fatal(err)
	}
	if err := driver.state(run, 4, protocol.StateIdle); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	app.TypeLine(session, "investigate the webhook timeout")
	if err := reportPrompt(driver, run.SessionID, run.RunID, "investigate the webhook timeout"); err != nil {
		t.Fatal(err)
	}
	task := w.HeadlessTask()
	if !strings.Contains(task.Prompt, "investigate the webhook timeout") {
		t.Fatalf("the title task asked %q, want the later user prompt instead of the notification ring", task.Prompt)
	}
	task.Answer("Investigate webhook timeout")
	awaitLabel(app, session, "Investigate webhook timeout")
}
