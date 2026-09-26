package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestReloadResumesALiveConversationWithTheSameLaunch(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	executable := attachRevivePinnedClaude(t, w)
	pinned := func(m *protocol.SpawnSessionMessage) {
		m.YoloMode = protocol.Ptr(true)
		m.Executable = protocol.Ptr(executable)
		m.Model = protocol.Ptr("claude-sonnet-5")
		m.Effort = protocol.Ptr("high")
	}

	talked := w.Spawn(app, fakeagent.Claude, w.Path("shop"), pinned)
	first := w.Launched(talked)
	reloadConverse(t, app, talked, first)
	app.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: talked, Cols: 91, Rows: 33})
	testworld.Await(app, protocol.EventPtyResized, func(e protocol.WebSocketEvent) bool {
		return protocol.Deref(e.ID) == talked && protocol.Deref(e.Cols) == 91
	})
	reloadRespawned(t, app, talked)
	reloaded := w.Launched(talked)
	if !reloaded.Resumed || reloaded.ConversationID != first.ConversationID {
		t.Errorf("the reload ran claude %q, want it resuming conversation %s", reloaded.Argv, first.ConversationID)
	}
	model, _ := flagValue(reloaded.Argv, "--model")
	effort, _ := flagValue(reloaded.Argv, "--effort")
	if reloaded.Argv[0] != executable || !slices.Contains(reloaded.Argv, "--dangerously-skip-permissions") || model != "claude-sonnet-5" || effort != "high" {
		t.Errorf("the reload ran claude %q, want the pinned executable skipping permissions with model claude-sonnet-5 and effort high", reloaded.Argv)
	}
	if attached := kittyAttach(app, talked); protocol.Deref(attached.Cols) != 91 || protocol.Deref(attached.Rows) != 33 {
		t.Errorf("the reloaded session is %dx%d, want the live 91x33 it had before the reload", protocol.Deref(attached.Cols), protocol.Deref(attached.Rows))
	}

	silent := w.Spawn(app, fakeagent.Claude, w.Path("blog"))
	w.Launched(silent)
	reloadRespawned(t, app, silent)
	if fresh := w.Launched(silent); fresh.Resumed {
		t.Errorf("reloading a session with no conversation ran claude %q, want a fresh launch", fresh.Argv)
	}

	for _, e := range app.Received() {
		if e.Event == protocol.EventSessionExited {
			t.Errorf("a reload told the app session %s exited", protocol.Deref(e.ID))
		}
	}
}

func TestReloadRelaunchesAnExitedSessionAtTheClientsGeometry(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.YoloMode = protocol.Ptr(true)
		m.Model = protocol.Ptr("claude-sonnet-5")
		m.Effort = protocol.Ptr("high")
	})
	w.Launched(session).Exit(0)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })

	for _, tc := range []struct {
		name, id, refusal string
	}{
		{"an exited session without geometry", session, "geometry"},
		{"an unknown session", "no-such-session", "session not found"},
	} {
		refused := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: tc.id},
			protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return r.ID == tc.id })
		if refused.Success || !strings.Contains(protocol.Deref(refused.Error), tc.refusal) {
			t.Errorf("reloading %s answered %+v, want a refusal mentioning %q", tc.name, refused, tc.refusal)
		}
	}

	reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: session, Cols: 91, Rows: 33},
		protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return r.ID == session })
	if !reloaded.Success {
		t.Fatalf("reloading the exited session with geometry: %s", protocol.Deref(reloaded.Error))
	}
	relaunched := w.Launched(session)
	model, _ := flagValue(relaunched.Argv, "--model")
	effort, _ := flagValue(relaunched.Argv, "--effort")
	if !slices.Contains(relaunched.Argv, "--dangerously-skip-permissions") || model != "claude-sonnet-5" || effort != "high" {
		t.Errorf("the relaunch ran claude %q, want it skipping permissions with model claude-sonnet-5 and effort high", relaunched.Argv)
	}
	if attached := kittyAttach(app, session); protocol.Deref(attached.Cols) != 91 || protocol.Deref(attached.Rows) != 33 {
		t.Errorf("the relaunched session is %dx%d, want the client's 91x33", protocol.Deref(attached.Cols), protocol.Deref(attached.Rows))
	}
}

func TestReloadKeepsTheApprovalItStartedWith(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "auto_approve_enabled", "true")
	reviewed := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(reviewed)
	setSetting(t, app, "auto_approve_enabled", "false")
	asked := w.Spawn(app, fakeagent.Claude, w.Path("blog"))
	w.Launched(asked)
	automated := attachReviveAutomationSession(t, w, app)
	w.Launched(automated)
	setSetting(t, app, "auto_approve_enabled", "true")

	reloadedRuns := map[string]*fakeagent.Run{}
	for _, tc := range []struct {
		name, session, mode string
		model, effort       string
		exit                bool
	}{
		{name: "a session launched under the reviewer", session: reviewed, mode: "auto"},
		{name: "a session launched asking the user", session: asked},
		{name: "an automation's agent", session: automated, mode: "auto", model: "sonnet", effort: "high"},
		{name: "an automation's exited agent", session: automated, mode: "auto", model: "sonnet", effort: "high", exit: true},
	} {
		if tc.exit {
			reloadedRuns[tc.session].Exit(0)
			testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == tc.session })
			launchIntentReload(t, app, tc.session)
		} else {
			reloadRespawned(t, app, tc.session)
		}
		reloadedRuns[tc.session] = w.Launched(tc.session)
		argv := reloadedRuns[tc.session].Argv
		mode, _ := flagValue(argv, "--permission-mode")
		model, _ := flagValue(argv, "--model")
		effort, _ := flagValue(argv, "--effort")
		if mode != tc.mode || model != tc.model || effort != tc.effort || slices.Contains(argv, "--dangerously-skip-permissions") {
			t.Errorf("reloading %s ran claude %q, want permission mode %q, model %q and effort %q", tc.name, argv, tc.mode, tc.model, tc.effort)
		}
	}
}

func TestChangingTheChiefRelaunchesExactlyTheAffectedAgents(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	runs := map[string]*fakeagent.Run{}
	conversing := func(dir string) string {
		id := w.Spawn(app, fakeagent.Claude, w.Path(dir))
		runs[id] = w.Launched(id)
		reloadConverse(t, app, id, runs[id])
		return id
	}
	alice, bob, carol := conversing("alice"), conversing("bob"), conversing("carol")
	shell := w.Spawn(app, workspaceShell, w.Path("dora"))
	runs[carol].Exit(0)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == carol })

	relaunchedAs := func(what, session string, chief bool) {
		t.Helper()
		testworld.Await(app, protocol.EventRuntimeRespawned, func(e protocol.WebSocketEvent) bool { return protocol.Deref(e.ID) == session })
		run := w.Launched(session)
		instructions, _ := flagValue(run.Argv, "--append-system-prompt")
		if !run.Resumed || run.ConversationID != runs[session].ConversationID || strings.Contains(instructions, "You are the chief of staff") != chief {
			t.Errorf("%s relaunched %s as %q, want it resuming %s with chief guidance %t", what, session, run.Argv, runs[session].ConversationID, chief)
		}
	}

	reloadSetChief(t, app, alice, true)
	relaunchedAs("assigning alice", alice, true)
	reloadSetChief(t, app, bob, true)
	relaunchedAs("transferring to bob", alice, false)
	relaunchedAs("transferring to bob", bob, true)
	reloadSetChief(t, app, bob, true)
	reloadSetChief(t, app, alice, false)
	reloadSetChief(t, app, bob, false)
	relaunchedAs("demoting bob", bob, false)
	reloadSetChief(t, app, carol, true)
	reloadSetChief(t, app, shell, true)
	reloadSetChief(t, app, shell, false)
	reloadSetChief(t, app, alice, true)
	relaunchedAs("assigning alice again", alice, true)

	respawns := map[string]int{}
	for _, e := range app.Received() {
		if e.Event == protocol.EventRuntimeRespawned {
			respawns[protocol.Deref(e.ID)]++
		}
	}
	if respawns[alice] != 3 || respawns[bob] != 2 || respawns[carol] != 0 || respawns[shell] != 0 {
		t.Errorf("runtime respawns alice=%d bob=%d carol=%d shell=%d, want 3, 2 and none for the exited carol or the shell", respawns[alice], respawns[bob], respawns[carol], respawns[shell])
	}
}

func reloadConverse(t *testing.T, app *testworld.Peer, session string, run *fakeagent.Run) {
	t.Helper()
	app.TypeLine(session, "add a discount field to checkout")
	run.Prompted()
	run.Reply("Done. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func reloadRespawned(t *testing.T, app *testworld.Peer, session string) {
	t.Helper()
	reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: session, Cols: 100, Rows: 30},
		protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return r.ID == session })
	if !reloaded.Success {
		t.Fatalf("reload %s: %s", session, protocol.Deref(reloaded.Error))
	}
	testworld.Await(app, protocol.EventRuntimeRespawned, func(e protocol.WebSocketEvent) bool { return protocol.Deref(e.ID) == session })
}

func reloadSetChief(t *testing.T, app *testworld.Peer, session string, chief bool) {
	t.Helper()
	if result := setChiefOfStaff(app, session, chief); !result.Success {
		t.Fatalf("set chief of staff %s=%t: %s", session, chief, protocol.Deref(result.Error))
	}
}
