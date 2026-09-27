package main_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

func TestADaemonRunsTasksOnlyWhereNoOtherProcessHoldsTheTaskRunnerLock(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	lockPath := filepath.Join(filepath.Dir(s.Socket), ".runner.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	holder, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if _, err := holder.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}

	refused := s.Run(testworld.Invocation{Args: []string{"daemon"}})
	if refused.Code != 1 || !strings.Contains(refused.Stderr, lockPath) || !strings.Contains(refused.Stderr, "held by pid "+strconv.Itoa(os.Getpid())) {
		t.Errorf("attn daemon beside a held task runner lock exited %d with %q, want 1 naming %s and its holder", refused.Code, refused.Stderr, lockPath)
	}

	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	s.Start()
	s.Kill()
	s.Start()
	s.Stop()

	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, lockPath); err != nil {
		t.Fatal(err)
	}
	if linked := s.Run(testworld.Invocation{Args: []string{"daemon"}}); linked.Code != 1 || !strings.Contains(linked.Stderr, lockPath) {
		t.Errorf("attn daemon beside a task runner lock that is a link exited %d with %q, want 1 naming %s", linked.Code, linked.Stderr, lockPath)
	}
	if kept, err := os.ReadFile(target); err != nil || string(kept) != "keep me" {
		t.Errorf("the link's target holds %q (%v), want it untouched", kept, err)
	}
}
