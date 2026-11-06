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

func TestAProgramInAShellTileReportsItsStateUntilTheShellPromptReturns(t *testing.T) {
	t.Parallel()
	for _, backend := range testworld.WorkerAndSharedHost {
		t.Run(string(backend), func(t *testing.T) {
			t.Parallel()
			s := testworld.NewStack(t, testworld.OnPTYBackend(backend))
			s.Start()
			app := s.App()
			shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
			testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
			answered, next := filepath.Join(s.Dir, "answered"), filepath.Join(s.Dir, "next")
			for _, fifo := range []string{answered, next} {
				if err := syscall.Mkfifo(fifo, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			app.TypeLine(shell, `sh -c "printf '\033]133;D;0\007\033]7501;state=blocked:kind=permission:msg=deploy?\007'; cat `+answered+`"`)
			asking := testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return x.State != protocol.SessionStateIdle })
			if asking.State != protocol.SessionStatePendingApproval {
				t.Fatalf("while a program in the shell reports a permission prompt, in the same output as the previous command's end, the tile is %s (%s), want pending_approval",
					asking.State, protocol.Deref(asking.StateReason))
			}

			s.Stop()
			s.Start()
			app = s.App()
			if restarted := initialSession(t, app, shell); restarted.State != protocol.SessionStatePendingApproval {
				t.Fatalf("after a daemon restart the tile is %s (%s), want the program's pending_approval",
					restarted.State, protocol.Deref(restarted.StateReason))
			}

			if err := os.WriteFile(answered, []byte("yes\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })

			app.TypeLine(shell, "cat "+next)
			testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
			if err := os.WriteFile(next, []byte("done\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			testworld.AwaitSession(app, shell, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
		})
	}
}
