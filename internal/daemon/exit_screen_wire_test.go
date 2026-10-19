package daemon_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnExitReleasesItsRuntimeWhenStorageIsUnavailable(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		launched := make(chan *testworld.Terminal, 1)
		w.terms.OnNextSpawn(func(terminal *testworld.Terminal) { launched <- terminal })
		session := w.Spawn(app, fakeagent.Claude, w.Path("exit-storage"))
		terminal := <-launched
		synctest.Wait()
		restore := w.RefuseDatabaseCommits()
		defer restore()
		terminal.Exit(1)
		synctest.Wait()
		restore()
		if _, err := w.terms.SessionInfo(context.Background(), terminal.Options.ID); !errors.Is(err, pty.ErrSessionNotFound) {
			t.Fatalf("exited runtime remains after storage failure: %v", err)
		}
		exited := testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
		if exited.ExitCode != 1 {
			t.Fatalf("exit code = %d, want 1", exited.ExitCode)
		}
	})
}

func TestPeekShowsTheLastExitAndALaterExitReplacesIt(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()
	cwd := w.Path("deploy")
	session := w.Spawn(app, fakeagent.Claude, cwd)

	first := w.Launched(session)
	app.TypeLine(session, "deploy it")
	first.Prompted()
	first.Reply("Error: the registry refused the push <!-- attn:state=idle -->")
	first.Exit(1)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
	earlier, _ := peekExit(t, cli, session, "the registry refused the push")
	if earlier.Code != 1 || earlier.At == "" {
		t.Errorf("first exit = %+v, want code 1 with its time", earlier)
	}

	w.Spawn(app, fakeagent.Claude, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ID = protocol.SessionID(session)
		m.ResumeSessionID = protocol.Ptr(session)
	})
	second := w.Launched(session)
	app.TypeLine(session, "try again")
	second.Prompted()
	second.Reply("Error: the disk is full <!-- attn:state=idle -->")
	second.Exit(2)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
	later, screen := peekExit(t, cli, session, "the disk is full")
	if later.Code != 2 || later.At < earlier.At {
		t.Errorf("second exit = %+v after %+v, want code 2 replacing the first", later, earlier)
	}
	if strings.Contains(screen, "the registry refused the push") {
		t.Errorf("peek after the second exit still shows the first one's screen: %q", screen)
	}
}

func peekExit(t *testing.T, cli *client.Client, session, showing string) (protocol.AgentPeekExit, string) {
	t.Helper()
	peek, err := cli.AgentPeek(session)
	if err != nil {
		t.Fatalf("peek %s: %v", session, err)
	}
	if peek.Exit == nil {
		t.Fatalf("peek of the exited %s = %+v, want its exit", session, peek)
	}
	if peek.Screen == nil || !strings.Contains(peek.Screen.Text, showing) {
		t.Fatalf("peek screen = %+v, want the final screen showing %q", peek.Screen, showing)
	}
	return *peek.Exit, peek.Screen.Text
}

func TestASpawnThatExitsImmediatelyKeepsItsNewScreenAcrossRestart(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		w.terms.OnNextSpawn(func(term *testworld.Terminal) {
			term.PaintScreen("Cannot start: missing credentials")
			term.Exit(1)
		})
		session := w.Spawn(app, fakeagent.Claude, w.Path("quick-exit"))
		for _, restart := range []bool{false, true} {
			if restart {
				w.restart()
			}
			exit, _ := peekExit(t, w.Client(), session, "missing credentials")
			if exit.Code != 1 {
				t.Errorf("quick exit after restart=%v = %+v", restart, exit)
			}
		}
		w.terms.OnNextSpawn(func(term *testworld.Terminal) {
			term.PaintScreen("Cannot start: provider unavailable")
			term.Exit(1)
		})
		app = w.App()
		w.Spawn(app, fakeagent.Claude, w.Path("quick-exit"), func(m *protocol.SpawnSessionMessage) {
			m.ID = protocol.SessionID(session)
		})
		for _, restart := range []bool{false, true} {
			if restart {
				w.restart()
			}
			exit, screen := peekExit(t, w.Client(), session, "provider unavailable")
			if exit.Code != 1 || strings.Contains(screen, "missing credentials") {
				t.Errorf("same-code respawn after restart=%v = %+v %q, want its new screen", restart, exit, screen)
			}
		}
	})
}
