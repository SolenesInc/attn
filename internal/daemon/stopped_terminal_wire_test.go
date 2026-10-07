package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAStoppedAgentKeepsItsScreenAcrossReconnectAndRestartUntilResumed(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.Model = protocol.Ptr("sonnet")
		m.Effort = protocol.Ptr("high")
	})
	run := w.Launched(session)
	app.TypeLine(session, "check the migration")
	run.Prompted()
	run.Reply("Migration checked. <!-- attn:state=idle -->")
	run.Exit(143)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })

	for _, restart := range []bool{false, true} {
		app.Close()
		if restart {
			w.restart()
		}
		app = w.App()
		state := queriedSession(t, w.Client(), session)
		if state.TerminalExit == nil || state.TerminalExit.Code != 143 || state.TerminalExit.At == "" {
			t.Fatalf("stopped agent after restart=%v: %+v", restart, state)
		}
		terminal := app.Terminal(session)
		result := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal), AttachPolicy: protocol.Ptr(protocol.AttachPolicySameAppRemount)},
			protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
		if !result.Success || protocol.Deref(result.Running) || result.Exit == nil || result.Exit.Code != 143 || result.Screen == nil || !strings.Contains(result.Screen.Text, "Migration checked") {
			t.Fatalf("stopped screen after restart=%v: %+v", restart, result)
		}
	}

	resumed := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30},
		protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
	if !resumed.Success {
		t.Fatal(protocol.Deref(resumed.Error))
	}
	later := w.Launched(session)
	if !later.Resumed || later.ConversationID != run.ConversationID || !slices.Contains(later.Argv, "sonnet") || !slices.Contains(later.Argv, "high") {
		t.Errorf("resumed launch = %+v, want the same conversation and settings", later)
	}
	if state := queriedSession(t, w.Client(), session); state.TerminalExit != nil {
		t.Errorf("resumed agent still carries its previous exit: %+v", state.TerminalExit)
	}
}

func TestACleanAgentQuitClosesWithoutAnAppRequest(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	run := w.Launched(session)
	app.Close()
	observer := w.App()
	run.Exit(0)
	awaitClosed(observer, session)
	if ids := queriedIDs(t, w.Client(), ""); slices.Contains(ids, session) {
		t.Fatalf("normally exited session is still live: %v", ids)
	}
	w.restart()
	if ids := ledgerIDs(ledger(t, w.Client(), client.SessionListOptions{Closed: true})); !slices.Contains(ids, session) {
		t.Errorf("normal quit missing from closed history: %v", ids)
	}
}

func TestStoppingAProcessThatExitsCleanlyKeepsItsTile(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	spawn, _, _ := w.RequestSpawn(app, shellHarness, w.Path("shop"))
	if !spawn.Success {
		t.Fatal(protocol.Deref(spawn.Error))
	}
	session := string(spawn.ID)
	app.TypeLine(session, `exec sh -c 'trap "exit 0" HUP TERM; echo ready-to-stop-$((6*7)); while :; do sleep 1; done'`)
	app.AwaitScreen(session, "ready-to-stop-42")
	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: protocol.SessionID(session)})
	exit := testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
	if exit.ExitCode != 0 {
		t.Fatalf("test process exited %d, want a caught stop returning 0", exit.ExitCode)
	}
	state := queriedSession(t, w.Client(), session)
	if state.TerminalExit == nil || state.TerminalExit.Code != 0 {
		t.Fatalf("explicitly stopped session no longer has its stopped tile: %+v", state)
	}
	w.restart()
	if state := queriedSession(t, w.Client(), session); state.TerminalExit == nil || state.TerminalExit.Code != 0 {
		t.Fatalf("restarting closed the explicitly stopped session: %+v", state)
	}
}
