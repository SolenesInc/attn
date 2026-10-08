package daemon_test

import (
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"

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

func TestClosingAStoppedAgentsTileClosesTheSession(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	run := w.Launched(session)
	terminal := app.Terminal(session)
	run.Exit(143)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })

	desktop, tile := tileOf(t, w, app, terminal)
	if closed := closeTileFromApp(app, desktop, tile); !closed.Success {
		t.Fatalf("close the stopped agent's tile: %s", protocol.Deref(closed.Error))
	}
	if entry := ledgerShowOverTheWebSocket(app, session).Entry; entry == nil || protocol.Deref(entry.ClosedAt) == "" {
		t.Errorf("closing the stopped agent's tile left %+v, want %s closed", entry, session)
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

func TestAPluginThatQuitsCleanlyDuringResumeClosesWithoutBlocking(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"resume": true})
		awaitDriverAvailable(app, driver.agent)
		cwd := w.Path("shop")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		session, _ := spawnDriven(w, app, driver, cwd)
		w.terminal(session).Exit(143)
		testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
		w.terms.OnNextSpawn(func(term *testworld.Terminal) { term.Exit(0) })
		result := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 80, Rows: 24}, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
		if !result.Success {
			t.Fatal(protocol.Deref(result.Error))
		}
		awaitClosed(app, session)
		if ids := queriedIDs(t, w.Client(), ""); slices.Contains(ids, session) {
			t.Fatalf("normally exited plugin is still live: %v", ids)
		}
		if ids := ledgerIDs(ledger(t, w.Client(), client.SessionListOptions{Closed: true})); !slices.Contains(ids, session) {
			t.Fatalf("normally exited plugin is missing from history: %v", ids)
		}
	})
}

func TestStoppingAPluginDuringResumeKeepsItsTileWhenItExitsCleanly(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"resume": true})
		awaitDriverAvailable(app, driver.agent)
		cwd := w.Path("shop")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		session, _ := spawnDriven(w, app, driver, cwd)
		w.terminal(session).Exit(143)
		testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
		started, release := make(chan struct{}), make(chan struct{})
		w.terms.OnNextSpawn(func(term *testworld.Terminal) {
			term.OnKill(func(syscall.Signal) { term.Exit(0) })
			close(started)
			<-release
		})
		app.Send(protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 80, Rows: 24})
		<-started
		app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: protocol.SessionID(session)})
		synctest.Wait()
		close(release)
		result := testworld.Await(app, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
		if !result.Success {
			t.Fatal(protocol.Deref(result.Error))
		}
		exit := testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
		if exit.ExitCode != 0 {
			t.Fatalf("stopped plugin exited %d, want 0", exit.ExitCode)
		}
		state := queriedSession(t, w.Client(), session)
		if state.TerminalExit == nil || state.TerminalExit.Code != 0 {
			t.Fatalf("stopped plugin lost its tile: %+v", state)
		}
		w.restart()
		if state := queriedSession(t, w.Client(), session); state.TerminalExit == nil || state.TerminalExit.Code != 0 {
			t.Fatalf("restarting closed the stopped plugin: %+v", state)
		}
	})
}

func TestStoppingAReloadedAgentKeepsItsTileWhenTheOldExitArrivesLate(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		agent := w.bubbleClaude(t, app, "shop")
		releaseOldExit := agent.term.HoldExitDelivery()
		releaseStoppedExit := make(chan struct{})
		w.terms.OnNextSpawn(func(term *testworld.Terminal) {
			term.OnKill(func(syscall.Signal) { <-releaseStoppedExit; term.Exit(0) })
		})
		reloadRespawned(t, app, agent.id)
		app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: protocol.SessionID(agent.id)})
		synctest.Wait()
		releaseOldExit()
		synctest.Wait()
		close(releaseStoppedExit)
		exit := testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == agent.id })
		if exit.ExitCode != 0 {
			t.Fatalf("replacement exited %d, want its caught Stop returning 0", exit.ExitCode)
		}
		state := queriedSession(t, w.Client(), agent.id)
		if state.TerminalExit == nil || state.TerminalExit.Code != 0 {
			t.Fatalf("late old exit removed the stopped replacement: %+v", state)
		}
	})
}

func TestACleanChiefQuitKeepsItsRoleAndStoppedTile(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("chief"), func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) })
	w.Launched(session).Exit(0)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
	for _, restart := range []bool{false, true} {
		if restart {
			w.restart()
		}
		state := queriedSession(t, w.Client(), session)
		if !protocol.Deref(state.ChiefOfStaff) || state.TerminalExit == nil || state.TerminalExit.Code != 0 {
			t.Fatalf("Chief after restart=%v lost its role or stopped tile: %+v", restart, state)
		}
	}
	app = w.App()
	result := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 80, Rows: 24}, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
	if !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	w.Launched(session)
	state := queriedSession(t, w.Client(), session)
	if !protocol.Deref(state.ChiefOfStaff) || state.TerminalExit != nil {
		t.Fatalf("resumed Chief lost its role or remains stopped: %+v", state)
	}
}

func TestASecondFailedResumePublishesItsStoppedOutcome(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"resume": true, "state_reporting": true})
		awaitDriverAvailable(app, driver.agent)
		cwd := w.Path("shop")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		session, run := spawnDriven(w, app, driver, cwd)
		if err := driver.state(run, 1, protocol.StateIdle); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		w.terminal(session).Exit(1)
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.TerminalExit != nil && s.TerminalExit.Code == 1 })
		synctest.Wait()
		result := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 80, Rows: 24}, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
		if !result.Success {
			t.Fatal(protocol.Deref(result.Error))
		}
		synctest.Wait()
		w.terminal(session).Exit(2)
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.TerminalExit != nil && s.TerminalExit.Code == 2 })
	})
}
