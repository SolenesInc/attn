package testworld

import (
	"github.com/victorarias/attn/internal/procwait"
	"path/filepath"
	"syscall"

	"github.com/victorarias/attn/internal/fakeagent"
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
	for _, pid := range pids {
		if pid <= 0 {
			continue
		}
		if exited, err := procwait.Exited(pid, fakeagent.HangGuard); err != nil || !exited {
			s.T.Fatalf("process %d survived the reboot for %s: %v", pid, fakeagent.HangGuard, err)
		}
	}
	s.kit.AwaitExited()
}
