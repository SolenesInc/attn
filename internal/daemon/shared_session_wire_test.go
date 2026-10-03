package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/testworld"
)

// sharedSession is one Codex session running in two panes of a workspace, each pane its own
// terminal and Codex process on the same thread.
type sharedSession struct {
	id, workspace         string
	firstPane, secondPane string
	first, second         string
	firstRun, secondRun   *fakeagent.Run
}

// runInTwoTerminals spawns a session, then a second pane whose fresh Codex chat resumes its thread,
// which shows the session there too. It returns with the session working on a prompt from the second.
func runInTwoTerminals(w *world, app *testworld.Peer) sharedSession {
	w.T.Helper()
	cwd := w.Path("shop")
	spawned, workspace, firstPane := w.RequestSpawn(app, fakeagent.Codex, cwd)
	owner := spawned.ID
	first := app.Terminal(owner)
	running := w.Launched(owner)
	app.TypeLine(owner, "find the flaky test")
	running.Prompted()
	running.Reply("It races the tax lookup. <!-- attn:state=idle -->")
	flaky := running.ConversationID

	fresh, _, secondPane := w.RequestSpawn(app, fakeagent.Codex, cwd)
	current := fresh.ID
	second := app.Terminal(current)
	codex := w.Launched(current)
	app.TypeLineIn(second, "/resume "+flaky)
	codex.Prompted()
	app.TypeLineIn(second, "now fix it")
	if got := codex.Prompted(); got != "now fix it" || codex.ConversationID != flaky {
		w.T.Fatalf("codex took %q in conversation %s, want it in %s", got, codex.ConversationID, flaky)
	}
	if shown := awaitSuccessor(app, current); shown.ID != owner {
		w.T.Fatalf("/resume %s showed session %s, want %s, which runs it in another pane", flaky, shown.ID, owner)
	}
	awaitClosed(app, current)
	app.AwaitPanes("both panes showing session "+owner, func(panes []protocol.WorkspaceLayoutPane) bool {
		return showsIn(panes, owner, first) && showsIn(panes, owner, second)
	})
	testworld.AwaitSession(app, owner, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	codex.SessionID = owner
	return sharedSession{
		id: owner, workspace: workspace,
		firstPane: firstPane, secondPane: secondPane,
		first: first, second: second,
		firstRun: running, secondRun: codex,
	}
}

func showsIn(panes []protocol.WorkspaceLayoutPane, session, terminal string) bool {
	return slices.ContainsFunc(panes, func(p protocol.WorkspaceLayoutPane) bool {
		return protocol.Deref(p.SessionID) == session && protocol.Deref(p.RuntimeID) == terminal
	})
}

func placesTerminal(panes []protocol.WorkspaceLayoutPane, terminal string) bool {
	return slices.ContainsFunc(panes, func(p protocol.WorkspaceLayoutPane) bool { return protocol.Deref(p.RuntimeID) == terminal })
}

func placesSession(panes []protocol.WorkspaceLayoutPane, session string) bool {
	return slices.ContainsFunc(panes, func(p protocol.WorkspaceLayoutPane) bool { return protocol.Deref(p.SessionID) == session })
}

func TestClosingOneOfTwoPanesKeepsTheSessionRunningInTheOther(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	shared := runInTwoTerminals(w, app)

	closePane(app, sessionPane{session: shared.id, workspace: shared.workspace, pane: shared.secondPane})
	app.AwaitPanes("only the first pane showing the session", func(panes []protocol.WorkspaceLayoutPane) bool {
		return showsIn(panes, shared.id, shared.first) && !placesTerminal(panes, shared.second)
	})
	app.TypeLineIn(shared.first, "run the suite")
	if got := shared.firstRun.Prompted(); got != "run the suite" {
		t.Fatalf("the first pane's codex received %q, want the line typed there", got)
	}
	shared.firstRun.Reply("Green except checkout. Retry it? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, shared.id, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	closePane(app, sessionPane{session: shared.id, workspace: shared.workspace, pane: shared.firstPane})
	awaitClosed(app, shared.id)
}

func TestOneOfTwoTerminalsExitingLeavesTheSessionRunningInTheOther(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	shared := runInTwoTerminals(w, app)

	shared.secondRun.Exit(0)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool {
		return e.ID == shared.second && e.SessionID == shared.id
	})
	app.AwaitPanes("the exited terminal's pane gone", func(panes []protocol.WorkspaceLayoutPane) bool {
		return showsIn(panes, shared.id, shared.first) && !placesTerminal(panes, shared.second)
	})
	app.TypeLineIn(shared.first, "run the suite")
	shared.firstRun.Prompted()
	shared.firstRun.Reply("Green except checkout. Retry it? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, shared.id, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}

func TestClosingASessionShownInTwoTerminalsEndsBothAndRemovesBothPanes(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	shared := runInTwoTerminals(w, app)
	bystander := w.Spawn(app, fakeagent.Codex, w.Path("shop"))

	app.Send(protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: shared.id})
	awaitClosed(app, shared.id)
	for _, terminal := range []string{shared.first, shared.second} {
		testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == terminal })
	}
	w.App().AwaitPanes("only the bystander's pane in the workspace", func(panes []protocol.WorkspaceLayoutPane) bool {
		return placesSession(panes, bystander) && !placesSession(panes, shared.id)
	})
}

func TestKillingASessionShownInTwoTerminalsEndsItInBoth(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	shared := runInTwoTerminals(w, app)

	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: shared.id})
	for _, terminal := range []string{shared.first, shared.second} {
		testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == terminal })
	}
	testworld.AwaitSession(app, shared.id, func(s protocol.Session) bool {
		return protocol.Deref(s.StateReason) == "process_exited"
	})
	app.AwaitPanes("one pane left showing the exited session, as a killed session's only pane stays", func(panes []protocol.WorkspaceLayoutPane) bool {
		return showsIn(panes, shared.id, shared.first) != showsIn(panes, shared.id, shared.second)
	})
}

func TestReloadingFromOnePaneRestartsOnlyThatPanesTerminal(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	shared := runInTwoTerminals(w, app)

	reloaded := testworld.Request(app, protocol.ReloadSessionMessage{
		Cmd: protocol.CmdReloadSession, ID: shared.id, Terminal: protocol.Ptr(shared.first), Cols: 100, Rows: 30,
	}, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return r.ID == shared.id })
	if !reloaded.Success {
		t.Fatalf("reloading the first pane failed: %s", protocol.Deref(reloaded.Error))
	}
	relaunched := w.LaunchedCarrying(shared.first)
	app.TypeLineIn(shared.first, "where were we")
	if got := relaunched.Prompted(); got != "where were we" {
		t.Fatalf("the relaunched codex received %q, want the line typed in its pane", got)
	}
	shared.secondRun.Reply("Fixed the race. <!-- attn:state=idle -->")
	app.TypeLineIn(shared.second, "run the suite")
	if got := shared.secondRun.Prompted(); got != "run the suite" {
		t.Fatalf("the second pane's codex received %q, want it still running and taking the line", got)
	}
}

func TestARestartWithOneOfTwoTerminalsDeadBringsTheSessionBackInTheLiveOne(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "worker")
	t.Setenv("ATTN_PTY_WORKER_BINARY", testworld.AttnBinary(t))
	w := newWorld(t, fakeagent.Codex)
	t.Cleanup(func() { ptyworker.ReapDataDir(w.Dir) })
	app := w.App()
	shared := runInTwoTerminals(w, app)

	w.stop()
	w.LoseTerminal(shared.second)
	w.start()
	app = w.App()
	app.AwaitPanes("only the live terminal's pane", func(panes []protocol.WorkspaceLayoutPane) bool {
		return showsIn(panes, shared.id, shared.first) && !placesTerminal(panes, shared.second)
	})
	app.TypeLineIn(shared.first, "run the suite")
	shared.firstRun.Prompted()
	shared.firstRun.Reply("Green except checkout. Retry it? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, shared.id, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}
