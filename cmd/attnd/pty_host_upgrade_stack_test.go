package main_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyhost/ptyhosttest"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/testworld"
)

func TestWithInPlaceUpgradesOffAnUpdatedTerminalStaysOnItsOldBuildAndShowsTheReloadNotice(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.StartBinary(testworld.AttnBinaryWithSnapshotFormat(t, "current"), "ATTN_PTY_HOST_BINARY="+ptyhosttest.Variant(t, "current"))
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
	app.TypeLine(shell, "echo started-$((1+1))")
	app.AwaitScreen(shell, "started-2")
	s.Stop()

	s.StartBinary(testworld.AttnBinaryWithSnapshotFormat(t, "next"), "ATTN_WORKER_INPLACE_UPGRADE=0", "ATTN_PTY_HOST_BINARY="+ptyhosttest.Variant(t, "next"))
	app = s.App()
	if came := initialSession(t, app, shell); !protocol.Deref(came.TerminalBuildStale) {
		t.Errorf("the terminal came back without the reload notice, want it offered while in-place upgrades are off")
	}
	terminal := app.Terminal(shell)
	attached := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
	if attached.Snapshot == nil || protocol.Deref(attached.Snapshot.Format) == "next" {
		t.Fatalf("the terminal attached with snapshot %+v, want it still on the old build", attached.Snapshot)
	}
	app.TypeLine(shell, "echo still-$((2+2))")
	app.AwaitScreen(shell, "still-4")
}

func TestAnAgentWhoseTerminalEndedWhileTheDaemonWasDownShowsNoReloadNotice(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	session := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	codex := s.Launched(session)
	app.TypeLine(session, "add a discount field")
	codex.Prompted()
	codex.Reply("Done. <!-- attn:state=idle -->")
	s.Stop()
	codex.Exit(143)

	s.Start()
	if came := initialSession(t, s.App(), session); came.TerminalBuildStale != nil {
		t.Errorf("the agent without a terminal came back with terminal_build_stale=%t, want no reload notice", *came.TerminalBuildStale)
	}
}

func TestASharedHostTerminalMovesToTheNewBuildAcrossAnUpdate(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	current := testworld.AttnBinaryWithSnapshotFormat(t, "current")
	next := testworld.AttnBinaryWithSnapshotFormat(t, "next")
	s.StartBinary(current, "ATTN_PTY_HOST_BINARY="+ptyhosttest.Variant(t, "current"))
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
	pidFile := filepath.Join(s.Dir, "shell.pid")
	app.TypeLine(shell, "echo $$ > "+pidFile+"; echo started-$((1+1))")
	app.AwaitScreen(shell, "started-2")
	shellBefore, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	hostBefore := sharedHostOf(t, s.Dir, app.Terminal(shell))
	s.Stop()

	s.StartBinary(next, "ATTN_PTY_HOST_BINARY="+ptyhosttest.Variant(t, "next"))
	app = s.App()
	terminal := app.Terminal(shell)
	attach := func() protocol.AttachResultMessage {
		return testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)},
			protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
	}
	attached := attach()
	if attached.Snapshot == nil || protocol.Deref(attached.Snapshot.Format) != "next" {
		testworld.AwaitEvent(app, "the attached stream of "+terminal+" to end when its host hands over", func(e protocol.WebSocketEvent) bool {
			return e.Event == protocol.EventPtyDesync && protocol.Deref(e.ID) == terminal
		})
		attached = attach()
	}
	if attached.Snapshot == nil || protocol.Deref(attached.Snapshot.Format) != "next" {
		t.Fatalf("after the update the terminal attached with snapshot %+v, want its host handed over to next", attached.Snapshot)
	}
	if host := sharedHostOf(t, s.Dir, terminal); host != hostBefore {
		t.Fatalf("the terminal moved from host pid %d to %d, want the same process carrying it", hostBefore, host)
	}
	if stale := initialSession(t, s.App(), shell).TerminalBuildStale; stale != nil {
		t.Errorf("after the handover the terminal shows terminal_build_stale=%t, want no reload notice", *stale)
	}
	app.TypeLine(shell, "echo $$ > "+pidFile+"; echo still-$((2+2))")
	app.AwaitScreen(shell, "still-4")
	if shellAfter, err := os.ReadFile(pidFile); err != nil || string(shellAfter) != string(shellBefore) {
		t.Fatalf("the shell is pid %q after the update (err %v), want the same shell %q", shellAfter, err, shellBefore)
	}
}

func sharedHostOf(t *testing.T, dataDir, terminal string) int {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(dataDir, "pty-hosts", "*", "registry", terminal+".json"))
	if len(paths) != 1 {
		t.Fatalf("shared host registry entries for %s: %v, want one", terminal, paths)
	}
	entry, err := ptyworker.ReadRegistry(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if syscall.Kill(entry.WorkerPID, 0) != nil {
		t.Fatalf("the host recorded for %s (pid %d) is not running", terminal, entry.WorkerPID)
	}
	return entry.WorkerPID
}

func initialSession(t *testing.T, app *testworld.Peer, id string) protocol.Session {
	t.Helper()
	for _, x := range app.Initial.Sessions {
		if string(x.ID) == id {
			return x
		}
	}
	t.Fatalf("session %s is missing from the initial state", id)
	return protocol.Session{}
}
