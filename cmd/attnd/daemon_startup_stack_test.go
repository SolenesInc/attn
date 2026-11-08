package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/testworld"
)

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

func TestADaemonThatCannotRunItsPTYBackendRefusesToStartAndSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  func(s *testworld.Stack) []string
		want []string
	}{
		{"embedded is gone", func(*testworld.Stack) []string {
			return []string{"ATTN_PTY_BACKEND=embedded"}
		}, []string{"ATTN_PTY_BACKEND=embedded is not supported", "worker, shared or migrating"}},
		{"the worker binary is missing", func(s *testworld.Stack) []string {
			return []string{"ATTN_PTY_BACKEND=worker", "ATTN_PTY_SKIP_STARTUP_PROBE=0", "ATTN_PTY_WORKER_BINARY=" + filepath.Join(s.Dir, "missing-attn-binary")}
		}, []string{"start the PTY workers: startup probe failed", "missing-attn-binary"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := testworld.NewStack(t)
			got := s.Run(testworld.Invocation{Args: []string{"daemon", "ensure"}, Env: tc.env(s)})
			if got.Code != 1 || !strings.Contains(got.Stderr, "daemon startup failed:") {
				t.Fatalf("attn daemon ensure exited %d with stderr %q, want the daemon's startup failure", got.Code, got.Stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(got.Stderr, want) {
					t.Errorf("attn daemon ensure said %q, want it to name %q", got.Stderr, want)
				}
			}
			if _, err := os.Stat(s.Socket); !os.IsNotExist(err) {
				t.Errorf("the refused daemon left its socket behind (stat: %v)", err)
			}
		})
	}
}
