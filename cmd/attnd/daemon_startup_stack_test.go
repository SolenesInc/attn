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
