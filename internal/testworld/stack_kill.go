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
	hostPaths, _ := filepath.Glob(filepath.Join(s.Dir, "pty-hosts", "*", "registry", "*.json"))
	paths = append(paths, hostPaths...)
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

func (s *Stack) KillRuntimeOf(terminal string) {
	s.T.Helper()
	path, err := s.registryPath(terminal)
	if err != nil {
		s.T.Fatalf("finding the runtime of terminal %s: %v", terminal, err)
	}
	entry, err := ptyworker.ReadRegistry(path)
	if err != nil {
		s.T.Fatalf("reading the registry of terminal %s: %v", terminal, err)
	}
	if err := syscall.Kill(entry.WorkerPID, syscall.SIGKILL); err != nil {
		s.T.Fatalf("killing runtime %d of terminal %s: %v", entry.WorkerPID, terminal, err)
	}
}
