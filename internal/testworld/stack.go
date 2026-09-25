package testworld

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/plugins"
	"github.com/victorarias/attn/internal/procreap"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/ptyworker"
)

type Stack struct {
	*World
	binary string
	daemon *os.Process
	exited chan error
}

type stackSetup struct {
	harnesses []fakeagent.Harness
}

type StackOption func(*stackSetup)

func WithAgents(h ...fakeagent.Harness) StackOption {
	return func(s *stackSetup) { s.harnesses = append(s.harnesses, h...) }
}

func NewStack(t *testing.T, opts ...StackOption) *Stack {
	t.Helper()
	if testing.Short() {
		t.Skip("a stack test runs the built attn binary, which -short does not build")
	}
	var setup stackSetup
	for _, opt := range opts {
		opt(&setup)
	}
	binary := AttnBinary(t)
	s := &Stack{World: Prepare(t, binary, setup.harnesses...), binary: binary}
	port := strconv.Itoa(reservePort(t))
	wsAddr := net.JoinHostPort("127.0.0.1", port)
	s.WSAddr = wsAddr
	s.Dial = func(ctx context.Context) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "tcp", wsAddr)
	}
	s.DialUnix = func() (net.Conn, error) { return net.Dial("unix", s.Socket) }
	s.Vars = append(s.Vars,
		"ATTN_HARNESS_DATA_DIR="+s.Dir,
		"ATTN_HARNESS_NOTEBOOK_ROOT="+filepath.Join(s.Dir, "notebook"),
		"ATTN_WS_PORT="+port,
		"ATTN_PTY_SKIP_STARTUP_PROBE=1",
		"ATTN_MOCK_GH_URL=http://127.0.0.1:1",
		"ATTN_PTY_BACKEND=worker",
	)
	t.Cleanup(s.reap)
	t.Cleanup(s.Stop)
	return s
}

func (s *Stack) Start() {
	s.T.Helper()
	if s.daemon != nil {
		s.T.Fatal("Start: the stack's daemon is already running")
	}
	ready, signal, err := os.Pipe()
	if err != nil {
		s.T.Fatal(err)
	}
	defer ready.Close()
	stderr, err := os.OpenFile(s.stderrPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		s.T.Fatal(err)
	}
	defer stderr.Close()
	cmd := exec.Command(s.binary, "daemon")
	cmd.Env = append(s.env(), "ATTN_DAEMON_READY_FD=3")
	dieWithTestProcess(cmd)
	cmd.ExtraFiles = []*os.File{signal}
	cmd.Stdout, cmd.Stderr = stderr, stderr
	err = cmd.Start()
	signal.Close()
	if err != nil {
		s.T.Fatalf("start attn daemon: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	s.daemon, s.exited = cmd.Process, exited

	line := make(chan string, 1)
	go func() {
		text, _ := bufio.NewReader(ready).ReadString('\n')
		line <- strings.TrimSuffix(text, "\n")
	}()
	select {
	case text := <-line:
		if text != "ready" {
			s.failStart(fmt.Sprintf("signalled %q instead of ready", text))
		}
	case <-time.After(fakeagent.HangGuard):
		s.failStart(fmt.Sprintf("was not ready within %s", fakeagent.HangGuard))
	}

	probe := s.App()
	for _, warning := range probe.Initial.Warnings {
		if strings.HasPrefix(warning.Code, "pty_backend_") {
			s.T.Fatalf("the daemon started on a fallback PTY backend: %s: %s", warning.Code, warning.Message)
		}
	}
	probe.Close()
}

func (s *Stack) failStart(reason string) {
	s.T.Helper()
	s.T.Errorf("attn daemon %s", reason)
	s.Stop()
	s.T.FailNow()
}

func (s *Stack) stderrPath() string {
	return filepath.Join(s.Dir, "daemon.stderr")
}

func (s *Stack) Stop() {
	if s.daemon == nil {
		return
	}
	s.ClosePeers()
	select {
	case err := <-s.exited:
		s.T.Errorf("attn daemon (pid %d) exited before Stop: %v", s.daemon.Pid, err)
	default:
		s.terminate()
	}
	s.daemon, s.exited = nil, nil
	if s.T.Failed() {
		s.LogDaemonTail()
		stderr, _ := os.ReadFile(s.stderrPath())
		s.T.Logf("daemon.stderr:\n%s", stderr)
	}
}

func (s *Stack) terminate() {
	_ = s.daemon.Signal(syscall.SIGTERM)
	select {
	case <-s.exited:
	case <-time.After(fakeagent.HangGuard):
		s.T.Errorf("attn daemon (pid %d) outlived SIGTERM by %s", s.daemon.Pid, fakeagent.HangGuard)
		_ = s.daemon.Kill()
		<-s.exited
	}
}

func (s *Stack) env() []string {
	var env []string
	for _, pair := range os.Environ() {
		if !ownedByAttnOrAnAgent(pair) {
			env = append(env, pair)
		}
	}
	return append(env, s.Vars...)
}

func ownedByAttnOrAnAgent(pair string) bool {
	key, _, _ := strings.Cut(pair, "=")
	return key == "CLAUDECODE" ||
		strings.HasPrefix(key, "ATTN_") ||
		strings.HasPrefix(key, "CLAUDE_CODE_") ||
		strings.HasPrefix(key, "CODEX_")
}

func (s *Stack) reap() {
	for _, r := range ptyworker.ReapDataDir(s.Dir) {
		if r.Outcome != ptyworker.ReapRemoved && r.Outcome != ptyworker.ReapAlreadyGone {
			s.T.Errorf("PTY worker for session %s (pid %d) %s: %v", r.SessionID, r.WorkerPID, r.Outcome, r.Err)
		}
	}
	reaped := append(ptyhost.ReapDataDir(s.Dir), plugins.ReapRuntimeProcesses(s.Dir)...)
	for _, r := range reaped {
		if r.Outcome != procreap.ReapTerminated && r.Outcome != procreap.ReapAlreadyGone {
			s.T.Errorf("process %s (pid %d) %s: %v", r.ID, r.PID, r.Outcome, r.Err)
		}
	}
}

var (
	reservedMu sync.Mutex
	reserved   = map[int]bool{}
)

func reservePort(t testing.TB) int {
	t.Helper()
	reservedMu.Lock()
	defer reservedMu.Unlock()
	for {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		if !reserved[port] {
			reserved[port] = true
			return port
		}
	}
}
