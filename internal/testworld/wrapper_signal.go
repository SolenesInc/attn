package testworld

import (
	"path/filepath"
	"syscall"

	"github.com/victorarias/attn/internal/ptyworker"
)

func (s *Stack) SignalAgentWrapper(session string, signal syscall.Signal) {
	s.T.Helper()
	terminal := s.Terminal(session)
	var paths []string
	for _, root := range []string{"workers", "pty-hosts"} {
		found, err := filepath.Glob(filepath.Join(s.Dir, root, "*", "registry", terminal+".json"))
		if err != nil {
			s.T.Fatal(err)
		}
		paths = append(paths, found...)
	}
	if len(paths) != 1 {
		s.T.Fatalf("terminal %s has registry entries %v; want one wrapper", terminal, paths)
	}
	entry, err := ptyworker.ReadRegistry(paths[0])
	if err != nil {
		s.T.Fatal(err)
	}
	if entry.ChildPID <= 0 {
		s.T.Fatalf("terminal %s has no wrapper PID", terminal)
	}
	if err := syscall.Kill(entry.ChildPID, signal); err != nil {
		s.T.Fatalf("signal wrapper %d for terminal %s: %v", entry.ChildPID, terminal, err)
	}
}
