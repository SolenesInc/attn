package testworld

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/victorarias/attn/internal/ptyworker"
)

func (s *Stack) SignalAgentWrapper(session string, signal syscall.Signal) {
	s.T.Helper()
	terminal := s.Terminal(session)
	path, err := s.registryPath(terminal)
	if err != nil {
		s.T.Fatal(err)
	}
	entry, err := ptyworker.ReadRegistry(path)
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

func (s *Stack) WrapperEvidence(session string) string {
	s.T.Helper()
	terminal := s.Terminal(session)
	path, err := s.registryPath(terminal)
	if err != nil {
		return err.Error()
	}
	var b strings.Builder
	if entry, err := ptyworker.ReadRegistry(path); err != nil {
		fmt.Fprintf(&b, "registry %s: %v\n", path, err)
	} else {
		fmt.Fprintf(&b, "registry %s: daemon_instance=%s worker_pid=%d child_pid=%d started_at=%s\n", path, entry.DaemonInstanceID, entry.WorkerPID, entry.ChildPID, entry.StartedAt)
	}
	instance := filepath.Dir(filepath.Dir(path))
	log := filepath.Join(instance, "log", terminal+".log")
	if filepath.Base(filepath.Dir(instance)) == "pty-hosts" {
		log = filepath.Join(instance, "log", "host.log")
	}
	if data, err := os.ReadFile(log); err != nil {
		fmt.Fprintf(&b, "log %s: %v\n", log, err)
	} else {
		fmt.Fprintf(&b, "log %s:\n%s", log, boundedDiagnostic(string(data)))
	}
	return b.String()
}

func (s *Stack) registryPath(terminal string) (string, error) {
	var paths []string
	for _, root := range []string{"workers", "pty-hosts"} {
		found, err := filepath.Glob(filepath.Join(s.Dir, root, "*", "registry", terminal+".json"))
		if err != nil {
			return "", err
		}
		paths = append(paths, found...)
	}
	if len(paths) != 1 {
		return "", fmt.Errorf("terminal %s has registry entries %v; want one wrapper", terminal, paths)
	}
	return paths[0], nil
}
