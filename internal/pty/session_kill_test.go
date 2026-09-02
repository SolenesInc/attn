package pty

import (
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The marker is emitted right after the script installs its signal traps: without waiting,
// kill() races the child's startup and the shell exits to the raw signal, silently passing.
func waitForKillReady(t *testing.T, s *Session, marker string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(s.ghostty.ViewportText(), marker) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for readiness marker %q", marker)
}

func spawnKillTestSession(t *testing.T, id, script string) (*Manager, *Session) {
	t.Helper()
	m := NewManager(nil)
	t.Cleanup(m.Shutdown)

	if err := m.Spawn(SpawnOptions{
		ID:              id,
		CWD:             t.TempDir(),
		Agent:           "probe-kill",
		ExternalCommand: []string{"/bin/bash", "-c", script},
		Cols:            80,
		Rows:            24,
	}); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	s, err := m.getSession(id)
	if err != nil {
		t.Fatalf("getSession() error: %v", err)
	}
	waitForKillReady(t, s, "__KILLREADY__")
	return m, s
}

func TestKill_SIGTERMIgnoringShellExitsViaSIGHUP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}

	_, s := spawnKillTestSession(t, "kill-term-ignored", `trap '' TERM; echo __KILLREADY__; while :; do sleep 0.1; done`)

	start := time.Now()
	var escalations []syscall.Signal
	if err := s.killWithEscalation(syscall.SIGTERM, 8*time.Second, func(sig syscall.Signal) {
		escalations = append(escalations, sig)
	}); err != nil {
		t.Fatalf("kill() error: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed >= 6*time.Second {
		t.Fatalf("kill() took %v, want well under the 8s deadline (should escalate to SIGHUP)", elapsed)
	}

	info := s.info()
	if info.ExitSignal == nil || *info.ExitSignal != syscall.SIGHUP.String() {
		got := "<nil>"
		if info.ExitSignal != nil {
			got = *info.ExitSignal
		}
		t.Fatalf("ExitSignal = %s, want %s", got, syscall.SIGHUP.String())
	}
	if len(escalations) != 1 || escalations[0] != syscall.SIGHUP {
		t.Fatalf("escalations = %v, want [%s]", escalations, syscall.SIGHUP)
	}
}

func TestKill_TERMAndHUPIgnoringChildFallsBackToSIGKILL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}

	_, s := spawnKillTestSession(t, "kill-term-hup-ignored", `trap '' TERM HUP; echo __KILLREADY__; while :; do sleep 0.1; done`)

	start := time.Now()
	var escalations []syscall.Signal
	if err := s.killWithEscalation(syscall.SIGTERM, 1500*time.Millisecond, func(sig syscall.Signal) {
		escalations = append(escalations, sig)
	}); err != nil {
		t.Fatalf("kill() error: %v", err)
	}
	elapsed := time.Since(start)

	info := s.info()
	if info.ExitSignal == nil || *info.ExitSignal != syscall.SIGKILL.String() {
		got := "<nil>"
		if info.ExitSignal != nil {
			got = *info.ExitSignal
		}
		t.Fatalf("ExitSignal = %s, want %s", got, syscall.SIGKILL.String())
	}
	if elapsed < 1200*time.Millisecond {
		t.Fatalf("kill() took %v, want it to ride the full ladder (>= ~1.2s)", elapsed)
	}
	if len(escalations) != 2 || escalations[0] != syscall.SIGHUP || escalations[1] != syscall.SIGKILL {
		t.Fatalf("escalations = %v, want [%s %s]", escalations, syscall.SIGHUP, syscall.SIGKILL)
	}
}

func TestKill_CooperativeChildExitsOnSIGTERMBeforeGrace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}

	_, s := spawnKillTestSession(t, "kill-term-cooperative", `echo __KILLREADY__; while :; do sleep 0.1; done`)

	start := time.Now()
	var escalations []syscall.Signal
	if err := s.killWithEscalation(syscall.SIGTERM, 8*time.Second, func(sig syscall.Signal) {
		escalations = append(escalations, sig)
	}); err != nil {
		t.Fatalf("kill() error: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed >= 1500*time.Millisecond {
		t.Fatalf("kill() took %v, want well under sigtermToHUPGrace (should not wait out the grace)", elapsed)
	}

	info := s.info()
	if info.ExitSignal == nil || *info.ExitSignal != syscall.SIGTERM.String() {
		got := "<nil>"
		if info.ExitSignal != nil {
			got = *info.ExitSignal
		}
		t.Fatalf("ExitSignal = %s, want %s", got, syscall.SIGTERM.String())
	}
	if len(escalations) != 0 {
		t.Fatalf("escalations = %v, want none", escalations)
	}
}

func TestManagerKill_InteractiveShellExitsBeforeTERMGrace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}

	tests := []struct {
		name string
		path string
		args []string
	}{
		{name: "bash", path: "/bin/bash", args: []string{"--noprofile", "--norc", "-i"}},
		{name: "zsh", path: "/bin/zsh", args: []string{"-f", "-i"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := os.Stat(tt.path); err != nil {
				if os.IsNotExist(err) {
					t.Skipf("%s unavailable", tt.path)
				}
				t.Fatalf("stat %s: %v", tt.path, err)
			}

			m := NewManager(nil)
			t.Cleanup(m.Shutdown)
			id := "close-interactive-" + tt.name
			command := append([]string{tt.path}, tt.args...)
			if err := m.Spawn(SpawnOptions{
				ID:              id,
				CWD:             t.TempDir(),
				Agent:           "probe-kill",
				ExternalCommand: command,
				LoginShellEnv:   os.Environ(),
			}); err != nil {
				t.Fatalf("Spawn() error: %v", err)
			}

			s, err := m.getSession(id)
			if err != nil {
				t.Fatalf("getSession() error: %v", err)
			}

			const marker = "__INTERACTIVE_SHELL_READY__"
			ready := make(chan struct{})
			var once sync.Once
			var outputMu sync.Mutex
			var output strings.Builder
			if _, err := m.Subscribe(id, "close-ready", func(data []byte, _ uint32) bool {
				outputMu.Lock()
				defer outputMu.Unlock()
				output.Write(data)
				if strings.Contains(output.String(), marker) {
					once.Do(func() { close(ready) })
				}
				return true
			}, nil); err != nil {
				t.Fatalf("Subscribe() error: %v", err)
			}
			if err := m.Input(id, []byte("printf '"+marker+"\\n'\r")); err != nil {
				t.Fatalf("Input() error: %v", err)
			}
			select {
			case <-ready:
			case <-time.After(defaultKillTimeout / 2):
				outputMu.Lock()
				defer outputMu.Unlock()
				t.Fatalf("timed out waiting for interactive shell readiness; output=%q", output.String())
			}
			s.agent = "shell"

			start := time.Now()
			if err := m.Kill(id, syscall.SIGTERM); err != nil {
				t.Fatalf("Kill() error: %v", err)
			}
			elapsed := time.Since(start)
			if elapsed >= sigtermToHUPGrace/2 {
				t.Fatalf("Kill() took %v, want under %v", elapsed, sigtermToHUPGrace/2)
			}
			t.Logf("interactive %s closed in %v", tt.name, elapsed)

			if info := s.info(); info.Running {
				t.Fatal("interactive shell still running after Kill()")
			}
		})
	}
}

func TestManagerKill_AgentUsesSIGTERM(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}

	m, s := spawnKillTestSession(t, "close-agent", `echo __KILLREADY__; while :; do sleep 0.1; done`)

	if err := m.Kill(s.id, syscall.SIGTERM); err != nil {
		t.Fatalf("Kill() error: %v", err)
	}
	info := s.info()
	if info.ExitSignal == nil || *info.ExitSignal != syscall.SIGTERM.String() {
		got := "<nil>"
		if info.ExitSignal != nil {
			got = *info.ExitSignal
		}
		t.Fatalf("ExitSignal = %s, want %s", got, syscall.SIGTERM.String())
	}
}
