package main_test

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/testworld"
)

func TestARestartKeepsALiveShellFinishesAnInterruptedCloseAndRevivesAShellWhoseWorkerDied(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	live := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("live"))
	app.TypeLine(live, "echo kept-$((1+1))")
	app.AwaitScreen(live, "kept-2")
	died := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("died"))
	diedTerminal := app.Terminal(died)
	closing := s.Spawn(app, fakeagent.Claude, s.Path("closing"))
	closingRun := s.Launched(closing)
	s.Stop()

	s.StartCrashingAt("session-teardown-before-kill")
	app = s.App()
	app.Send(protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: protocol.SessionID(closing)})
	s.AwaitCrash()
	killWorker(t, s.Dir, diedTerminal)

	s.Start()
	app = s.App()
	closingRun.AwaitGone()
	if x := initialSession(t, app, live); x.State == protocol.SessionStateRecoverable {
		t.Errorf("the live shell came back %s", x.State)
	}
	app.AwaitScreen(live, "kept-2")
	if x := initialSession(t, app, died); x.State != protocol.SessionStateRecoverable {
		t.Errorf("the shell whose worker died came back %s, want %s", x.State, protocol.SessionStateRecoverable)
	}
	for _, x := range app.Initial.Sessions {
		if string(x.ID) == closing {
			t.Errorf("the session closed before the crash came back in state %s", x.State)
		}
	}
}

func killWorker(t *testing.T, dataDir, terminal string) {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(dataDir, "workers", "*", "registry", terminal+".json"))
	if len(paths) != 1 {
		t.Fatalf("worker registry entries for %s: %v, want one", terminal, paths)
	}
	entry, err := ptyworker.ReadRegistry(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int{entry.ChildPID, entry.WorkerPID} {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			t.Fatalf("killing pid %d of terminal %s: %v", pid, terminal, err)
		}
	}
}
