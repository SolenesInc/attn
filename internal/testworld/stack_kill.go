package testworld

import (
	"path/filepath"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/procreap"
	"github.com/victorarias/attn/internal/ptyworker"
)

func (s *Stack) Kill() {
	s.T.Helper()
	if s.daemon == nil {
		s.T.Fatal("Kill: the stack's daemon is not running")
	}
	_ = s.daemon.Kill()
	<-s.exited
	s.ClosePeers()
	s.daemon, s.exited = nil, nil
}

func (s *Stack) Reboot() {
	s.T.Helper()
	s.Stop()
	paths, _ := filepath.Glob(filepath.Join(s.Dir, "workers", "*", "registry", "*.json"))
	var pids []int
	for _, path := range paths {
		if entry, err := ptyworker.ReadRegistry(path); err == nil {
			pids = append(pids, entry.WorkerPID, entry.ChildPID)
		}
	}
	for _, pid := range pids {
		if pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	deadline := time.Now().Add(fakeagent.HangGuard)
	for _, pid := range pids {
		for pid > 0 && procreap.ProcessAlive(pid) {
			if time.Now().After(deadline) {
				s.T.Fatalf("process %d survived the reboot", pid)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	s.kit.AwaitExited()
}
