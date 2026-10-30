package main_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestDaemonEnsureLeavesAPausedDaemonAndItsShellRunning(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Vars = append(s.Vars, "ATTN_INSTANCE=ensure-paused", "SHELL=/bin/bash")
	s.StartWith("ATTN_PTY_BACKEND=embedded")
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shell"))
	app.TypeLine(shell, "kept=before; printf '__ready_%s\\n' \"$kept\"")
	app.AwaitScreen(shell, "__ready_before")
	pidPath := filepath.Join(s.Dir, "attn.pid")
	pid, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	s.Signal(syscall.SIGSTOP)
	t.Cleanup(func() { s.Signal(syscall.SIGCONT) })
	for _, args := range [][]string{{"daemon", "ensure"}, {"_bridge"}} {
		got := s.Attn(args...)
		limit := regexp.MustCompile(`health_timeout=([0-9.]+[a-z]+)`).FindStringSubmatch(got.Stderr)
		if got.Code != 1 || !strings.Contains(got.Stderr, "pid "+strings.TrimSpace(string(pid))) || len(limit) != 2 ||
			!strings.Contains(got.Stderr, "ATTN_INSTANCE=ensure-paused attn daemon stop") || !strings.Contains(got.Stderr, "attn daemon ensure") {
			t.Fatalf("attn %v over a paused daemon: %+v; want refusal naming its PID, health timeout, and instance restart commands", args, got)
		}
		if wait, err := time.ParseDuration(limit[1]); err != nil || wait <= 0 {
			t.Fatalf("health timeout %q must name a positive duration: %v", limit[1], err)
		}
		t.Logf("attn %v: %s", args, strings.TrimSpace(got.Stderr))
	}
	s.Signal(syscall.SIGCONT)
	if got := s.Attn("daemon", "ensure"); got.Code != 0 || !strings.Contains(got.Stdout, `"already_running"`) {
		t.Fatalf("ensure after SIGCONT: %+v", got)
	}
	if after, err := os.ReadFile(pidPath); err != nil || string(after) != string(pid) {
		t.Fatalf("daemon PID after SIGCONT = %q, %v; want %q", after, err, pid)
	}
	app.TypeLine(shell, "printf '__survived_%s\\n' \"$kept\"")
	app.AwaitScreen(shell, "__survived_before")
	t.Logf("daemon pid %s and its embedded shell survived both Ensure callers", strings.TrimSpace(string(pid)))
}

func TestDaemonEnsureReplacesAConfirmedDifferentBuild(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.StartBinary(testworld.AttnUpdateBinary(t, "ensure-before", "stable", ""))
	pidPath := filepath.Join(s.Dir, "attn.pid")
	before, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	next := testworld.AttnUpdateBinary(t, "ensure-after", "stable", "")
	got := s.Run(testworld.Invocation{Binary: next, Args: []string{"daemon", "ensure"}})
	t.Cleanup(func() { s.Run(testworld.Invocation{Binary: next, Args: []string{"daemon", "stop"}}) })
	var result struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	got.JSON(t, &result)
	if got.Code != 0 || result.Status != "restarted" || result.Reason != "source_fingerprint_mismatch" {
		t.Fatalf("ensure from a different build: %+v", got)
	}
	s.AwaitStopped()
	after, err := os.ReadFile(pidPath)
	if err != nil || string(after) == string(before) {
		t.Fatalf("replacement PID = %q, %v; want a new PID instead of %q", after, err, before)
	}
	if got := s.Run(testworld.Invocation{Binary: next, Args: []string{"daemon", "ensure"}}); got.Code != 0 || !strings.Contains(got.Stdout, `"already_running"`) {
		t.Fatalf("ensure from the replacement build: %+v", got)
	}
	t.Logf("confirmed build mismatch replaced pid %s with pid %s", strings.TrimSpace(string(before)), strings.TrimSpace(string(after)))
}

func TestDaemonEnsureStartsOneDaemonForRacingCallersAndSaysWhyOneCannotStart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	ensure := func() (status string, got testworld.Result) {
		t.Helper()
		got = s.Attn("daemon", "ensure")
		var result struct {
			Status string `json:"status"`
		}
		if got.Code == 0 {
			got.JSON(t, &result)
		}
		return result.Status, got
	}

	// Without fd inheritance, ensure must refuse the socket held by the stack.
	if got := s.Run(testworld.Invocation{Args: []string{"daemon", "ensure"}, Env: []string{"ATTN_HARNESS_WS_LISTENER_FD="}}); got.Code != 1 || !strings.Contains(got.Stderr, "daemon ensure error: daemon startup failed: refusing to start: cannot bind the WebSocket address "+s.WSAddr) {
		t.Errorf("attn daemon ensure with its port taken exited %d with stderr %q, want the daemon's own startup failure", got.Code, got.Stderr)
	}

	t.Cleanup(func() { s.Attn("daemon", "stop") })
	statuses := make([]string, 3)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Go(func() {
			var got testworld.Result
			if statuses[i], got = ensure(); got.Code != 0 {
				t.Errorf("a racing attn daemon ensure exited %d: %s", got.Code, got.Stderr)
			}
		})
	}
	wg.Wait()
	slices.Sort(statuses)
	if want := []string{"already_running", "already_running", "started"}; !slices.Equal(statuses, want) {
		t.Errorf("three racing attn daemon ensure calls reported %q, want %q", statuses, want)
	}
	if status, _ := ensure(); status != "already_running" {
		t.Errorf("attn daemon ensure over the running daemon reported %q, want already_running", status)
	}

	if err := os.Remove(s.Socket); err != nil {
		t.Fatal(err)
	}
	if _, got := ensure(); got.Code != 1 || !strings.Contains(got.Stderr, "missing its Unix listener") {
		t.Errorf("attn daemon ensure over a daemon that lost its socket exited %d with stderr %q, want a refusal naming the missing listener", got.Code, got.Stderr)
	}
}
