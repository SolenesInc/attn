package main_test

import (
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/procwait"
	"github.com/victorarias/attn/internal/testworld"
)

func TestDaemonStopKeepsTerminalsRunningUntilAskedToEndThem(t *testing.T) {
	for _, backend := range testworld.WorkerAndSharedHost {
		t.Run(string(backend), func(t *testing.T) {
			t.Parallel()
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex), testworld.OnPTYBackend(backend))
			s.Start()
			app := s.App()
			agent := s.Launched(s.Spawn(app, fakeagent.Codex, s.Path("codex"))).PID

			stopped := s.Attn("daemon", "stop")
			if stopped.Code != 0 {
				t.Fatalf("attn daemon stop exited %d: %s", stopped.Code, stopped.Stderr)
			}
			s.AwaitStopped()
			if err := syscall.Kill(agent, 0); err != nil {
				t.Fatalf("agent %d did not outlive attn daemon stop: %v", agent, err)
			}
			if !strings.Contains(stopped.Stdout, "terminals keep running in pids [") || !strings.Contains(stopped.Stdout, "attn daemon stop --terminals") {
				t.Errorf("attn daemon stop printed %q, want it to say which runtimes keep the terminals and how to end them", stopped.Stdout)
			}

			ended := s.Attn("daemon", "stop", "--terminals")
			if ended.Code != 0 {
				t.Fatalf("attn daemon stop --terminals exited %d: %s\n%s", ended.Code, ended.Stdout, ended.Stderr)
			}
			if exited, err := procwait.Exited(agent, fakeagent.HangGuard); err != nil || !exited {
				t.Fatalf("agent %d survived attn daemon stop --terminals for %s (%v):\n%s", agent, fakeagent.HangGuard, err, ended.Stdout)
			}
			if again := s.Attn("daemon", "stop"); strings.Contains(again.Stdout, "terminals keep running") {
				t.Errorf("after --terminals, attn daemon stop still printed %q", again.Stdout)
			}
			t.Logf("agent %d ended; attn daemon stop --terminals printed:\n%s", agent, ended.Stdout)
		})
	}
}
