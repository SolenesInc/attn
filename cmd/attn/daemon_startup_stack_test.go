package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/testworld"
)

func TestADaemonWhosePortIsTakenExitsBeforeReadyNamingThePort(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	// Without fd inheritance, the daemon must refuse the socket held by the stack.
	got := s.Run(testworld.Invocation{Args: []string{"daemon"}, Env: []string{"ATTN_HARNESS_WS_LISTENER_FD="}})
	if got.Code != 1 || !strings.Contains(got.Stderr, s.WSAddr) {
		t.Fatalf("attn daemon with its port taken exited %d, want 1 naming %s\nstderr:\n%s", got.Code, s.WSAddr, got.Stderr)
	}
	if _, err := os.Stat(s.Socket); !os.IsNotExist(err) {
		t.Fatalf("the refused daemon left its socket behind (stat: %v), a false ready signal", err)
	}
}

func TestADaemonWhoseBackgroundJobsAlreadyRunElsewhereExitsBeforeReadyAndLetsTheNextOneStart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	holder, err := jobs.AcquireDirLock(filepath.Dir(s.Socket), nil)
	if err != nil {
		t.Fatalf("hold the runner lock: %v", err)
	}

	got := s.Attn("daemon")
	if got.Code != 1 || !strings.Contains(got.Stderr, "start background jobs") || !strings.Contains(got.Stderr, holder.Path()) {
		t.Fatalf("attn daemon beside a running job runner exited %d, want 1 naming the runner lock %s\nstderr:\n%s", got.Code, holder.Path(), got.Stderr)
	}
	if _, err := os.Stat(s.Socket); !os.IsNotExist(err) {
		t.Fatalf("the refused daemon left its socket behind (stat: %v)", err)
	}

	holder.Release()
	s.Start()
}

func TestTheDaemonRunsThePTYBackendItWasAskedForAndSaysWhich(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"worker", []string{"ATTN_PTY_BACKEND=worker"}, "worker"},
		{"embedded", []string{"ATTN_PTY_BACKEND=embedded"}, "embedded"},
		{"default", []string{"ATTN_PTY_BACKEND=", "ATTN_PTY_HOST_BINARY="}, "migrating"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := testworld.NewStack(t)
			s.Vars = append(s.Vars, tc.env...)
			s.Start()
			if got := s.App().Initial.Settings["pty_backend_mode"]; got != tc.want {
				t.Fatalf("the app is told the PTY backend is %v, want %s", got, tc.want)
			}
		})
	}
}

func TestADaemonWhosePTYWorkerCannotStartFallsBackToEmbeddedAndWarns(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Vars = append(s.Vars,
		"ATTN_PTY_BACKEND=worker",
		"ATTN_PTY_SKIP_STARTUP_PROBE=0",
		"ATTN_PTY_WORKER_BINARY="+filepath.Join(s.Dir, "missing-attn-binary"),
	)
	s.StartAcceptingAFallbackPTYBackend()
	app := s.App()
	if got := app.Initial.Settings["pty_backend_mode"]; got != "embedded" {
		t.Errorf("after the worker probe failed the app is told the PTY backend is %v, want embedded", got)
	}
	warned := false
	for _, warning := range app.Initial.Warnings {
		warned = warned || warning.Code == "pty_backend_fallback"
	}
	if !warned {
		t.Errorf("the app got warnings %+v, want pty_backend_fallback", app.Initial.Warnings)
	}
}
