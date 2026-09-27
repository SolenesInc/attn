package main_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAShellCommandThatEndedWhileTheDaemonWasDownComesBackIdle(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
	testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
	release, finished := filepath.Join(s.Dir, "release"), filepath.Join(s.Dir, "finished")
	for _, fifo := range []string{release, finished} {
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app.TypeLine(shell, "cat "+release+"; echo done > "+finished)
	testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	s.Stop()

	if err := os.WriteFile(release, []byte("go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(finished); err != nil {
		t.Fatal(err)
	}
	s.Start()
	app = s.App()
	for _, x := range app.Initial.Sessions {
		if x.ID == shell && x.State != protocol.SessionStateIdle {
			testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
		}
	}
}
