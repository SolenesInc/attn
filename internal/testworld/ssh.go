package testworld

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
)

type SSH struct {
	t           testing.TB
	Dir         string
	mu          sync.Mutex
	routes      map[string]string
	reachable   map[string]string
	machines    map[string]*sshMachine
	nextMachine int
}

type sshMachine struct {
	drain func()
	stack *Stack
	stop  func()
	link  *sshLink
}
type sshLink struct {
	mu   sync.Mutex
	gate *sshGate
}
type sshGate struct {
	entered chan struct{}
	enter   sync.Once
	done    chan struct{}
	err     error
	finish  func(error)
}

func newSSHGate() *sshGate {
	gate := &sshGate{done: make(chan struct{}), entered: make(chan struct{})}
	var once sync.Once
	gate.finish = func(err error) { once.Do(func() { gate.err = err; close(gate.done) }) }
	return gate
}
func newSSHLink() *sshLink {
	gate := newSSHGate()
	gate.finish(nil)
	return &sshLink{gate: gate}
}
func (l *sshLink) wait(ctx context.Context) error {
	l.mu.Lock()
	gate := l.gate
	l.mu.Unlock()
	gate.enter.Do(func() { close(gate.entered) })
	select {
	case <-gate.done:
		return gate.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type sshLinkWriter struct {
	ctx    context.Context
	link   *sshLink
	writer io.Writer
}

func (w sshLinkWriter) Write(p []byte) (int, error) {
	if err := w.link.wait(w.ctx); err != nil {
		return 0, err
	}
	return w.writer.Write(p)
}
func (s *SSH) Pause(destination string) func() {
	s.mu.Lock()
	machine := s.machines[destination]
	s.mu.Unlock()
	if machine == nil {
		s.t.Fatalf("no SSH machine for %s", destination)
	}
	machine.link.mu.Lock()
	gate := newSSHGate()
	machine.link.gate = gate
	machine.link.mu.Unlock()
	return func() { gate.finish(nil) }
}

func NewSSH(t testing.TB) *SSH {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "attn-ssh-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	s := &SSH{t: t, Dir: dir, routes: map[string]string{}, reachable: map[string]string{}, machines: map[string]*sshMachine{}}
	s.save()
	t.Cleanup(func() {
		paths, _ := filepath.Glob(filepath.Join(s.Dir, "masters", "*.sock"))
		for _, path := range paths {
			conn, err := net.Dial("unix", path)
			if err == nil {
				_, _ = io.WriteString(conn, "exit\n")
				_, _ = io.Copy(io.Discard, conn)
				conn.Close()
			}
		}
	})
	return s
}
func (s *SSH) save() {
	data, _ := json.Marshal(s.routes)
	if err := os.WriteFile(filepath.Join(s.Dir, "routes.json.next"), data, 0600); err != nil {
		s.t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(s.Dir, "routes.json.next"), filepath.Join(s.Dir, "routes.json")); err != nil {
		s.t.Fatal(err)
	}
}
func (s *SSH) Use(w *World) {
	w.T.Helper()
	binary, err := os.Executable()
	if err != nil {
		w.T.Fatal(err)
	}
	bin := filepath.Join(w.Dir, "ssh-bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		w.T.Fatal(err)
	}
	script := "#!/bin/sh\nATTN_TESTWORLD_SSH=client exec \"" + binary + "\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0700); err != nil {
		w.T.Fatal(err)
	}
	path := os.Getenv("PATH")
	for _, v := range w.Vars {
		if strings.HasPrefix(v, "PATH=") {
			path = strings.TrimPrefix(v, "PATH=")
		}
	}
	w.Vars = append(w.Vars, "PATH="+bin+":"+path, "ATTN_TESTWORLD_SSH_ROUTES="+filepath.Join(s.Dir, "routes.json"), "ATTN_TESTWORLD_SSH_CALLS="+filepath.Join(s.Dir, "calls.jsonl"), "ATTN_HARNESS_CONTROL_ROOT="+filepath.Join(s.Dir, "masters"))
}
func (s *SSH) Route(destination string, remote *Stack) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if machine := s.machines[destination]; machine != nil {
		if machine.stack == remote {
			return
		}
		machine.stop()
	}
	root := filepath.Join(remote.Dir, "install")
	if err := os.MkdirAll(filepath.Join(root, ".local", "bin"), 0700); err != nil {
		s.t.Fatal(err)
	}
	s.InstallBinary(remote, remote.binary)
	bin := filepath.Join(remote.Dir, "machine-bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		s.t.Fatal(err)
	}
	for name, body := range map[string]string{"uname": "echo Linux x86_64", "busctl": "echo b false"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
			s.t.Fatal(err)
		}
	}
	path := os.Getenv("PATH")
	for _, v := range remote.Vars {
		if strings.HasPrefix(v, "PATH=") {
			path = strings.TrimPrefix(v, "PATH=")
		}
	}
	machineEnv := []string{"PATH=" + bin + ":" + path, "ATTN_HARNESS_OUTPOST_INSTALL_ROOT=" + root}
	link := newSSHLink()
	socket := filepath.Join(s.Dir, fmt.Sprintf("machine-%d.sock", s.nextMachine))
	s.nextMachine++
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		s.t.Fatal(err)
	}
	lifeline, holder, err := os.Pipe()
	if err != nil {
		s.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			workers.Go(func() {
				defer conn.Close()
				stop := context.AfterFunc(ctx, func() { conn.Close() })
				defer stop()
				var req struct {
					Script, ResultFile string
					Lifeline           bool
				}
				if err := readSSHRequest(conn, &req); err != nil {
					return
				}
				if req.Lifeline {
					_, _ = io.Copy(io.Discard, conn)
					return
				}
				requestCtx, requestCancel := context.WithCancel(ctx)
				defer requestCancel()
				if err := link.wait(requestCtx); err != nil {
					result, _ := json.Marshal(struct {
						Code   int
						Stderr string
					}{255, err.Error()})
					_ = os.WriteFile(req.ResultFile, result, 0600)
					return
				}
				cmd := exec.CommandContext(requestCtx, "sh", "-c", req.Script)
				cmd.Env = append(append(append(remote.env(), remote.pauses.env()...), machineEnv...), "ATTN_HARNESS_LIFELINE_FD=3")
				cmd.ExtraFiles = []*os.File{lifeline}
				ws, err := remote.wsListener.File()
				if err != nil {
					return
				}
				defer ws.Close()
				cmd.ExtraFiles = append(cmd.ExtraFiles, ws)
				cmd.Env = append(cmd.Env, "ATTN_HARNESS_WS_LISTENER_FD=4")
				input, inputWriter, err := os.Pipe()
				if err != nil {
					return
				}
				defer input.Close()
				defer inputWriter.Close()
				copied := make(chan struct{})
				go func() {
					defer close(copied)
					_, _ = io.Copy(sshLinkWriter{requestCtx, link, inputWriter}, conn)
					inputWriter.Close()
				}()
				cmd.Stdin = input
				cmd.Stdout = sshLinkWriter{requestCtx, link, conn}
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				err = cmd.Run()
				requestCancel()
				conn.CloseRead()
				input.Close()
				<-copied
				code := 0
				if err != nil {
					code = -1
					if cmd.ProcessState != nil {
						code = cmd.ProcessState.ExitCode()
					}
				}
				result, _ := json.Marshal(struct {
					Code   int
					Stderr string
				}{code, stderr.String()})
				_ = os.WriteFile(req.ResultFile, result, 0600)
			})
		}
	}()
	stopMachine := sync.OnceFunc(func() {
		holder.Close()
		cancel()
		listener.Close()
		<-done
		workers.Wait()
		lifeline.Close()
	})
	s.t.Cleanup(stopMachine)
	s.machines[destination] = &sshMachine{stack: remote, stop: stopMachine, link: link, drain: workers.Wait}
	s.routes[destination] = socket
	s.reachable[destination] = socket
	s.save()
}

func (s *SSH) Restore(destination string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if machine := s.machines[destination]; machine != nil {
		machine.link.mu.Lock()
		machine.link.gate = newSSHLink().gate
		machine.link.mu.Unlock()
	}
	s.routes[destination] = s.reachable[destination]
	s.save()
}
func (s *SSH) Fail(destination, stderr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if machine := s.machines[destination]; machine != nil {
		machine.link.mu.Lock()
		gate := machine.link.gate
		machine.link.mu.Unlock()
		gate.finish(fmt.Errorf("%s", stderr))
	}
	s.routes[destination] = "error:" + stderr
	s.save()
}

func (s *SSH) RequireConfirmation(destination string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[destination] = "prompt:" + s.reachable[destination]
	s.save()
}

func (s *SSH) RequireAgent(socket string) {
	s.t.Helper()
	if err := os.WriteFile(filepath.Join(s.Dir, "agent-socket"), []byte(socket), 0600); err != nil {
		s.t.Fatal(err)
	}
}

func (s *SSH) MasterAttempts() int {
	s.t.Helper()
	data, err := os.ReadFile(filepath.Join(s.Dir, "calls.jsonl"))
	if err != nil && !os.IsNotExist(err) {
		s.t.Fatal(err)
	}
	count := 0
	for _, line := range bytes.Split(data, []byte("\n")) {
		var args []string
		if json.Unmarshal(line, &args) != nil {
			continue
		}
		master, batch := false, false
		for _, arg := range args {
			master = master || arg == "ControlMaster=yes"
			batch = batch || arg == "BatchMode=yes"
		}
		if master && batch {
			count++
		}
	}
	return count
}

func readSSHRequest(conn net.Conn, request any) error {
	var line []byte
	var next [1]byte
	for {
		if _, err := io.ReadFull(conn, next[:]); err != nil {
			return err
		}
		if next[0] == 10 {
			break
		}
		line = append(line, next[0])
	}
	return json.Unmarshal(line, request)
}

func (s *SSH) ExitMaster(home *Stack, host string) {
	home.T.Helper()
	path := filepath.Join(s.Dir, "masters", "default-"+strings.ReplaceAll(host, "-", "")[:12]+".sock")
	cmd := exec.Command(filepath.Join(home.Dir, "ssh-bin", "ssh"), "-o", "ControlPath="+path, "-O", "exit", "unused")
	cmd.Env = home.env()
	if out, err := cmd.CombinedOutput(); err != nil {
		home.T.Fatalf("exit fake master: %v: %s", err, out)
	}
}

func (s *SSH) InstallPath(remote *Stack) string {
	return filepath.Join(remote.Dir, "install", ".local", "bin", "attn")
}
func (s *SSH) InstallBinary(remote *Stack, binary string) {
	s.t.Helper()
	data, err := os.ReadFile(binary)
	if err != nil {
		s.t.Fatal(err)
	}
	target := s.InstallPath(remote)
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(target+".next", data, 0700); err != nil {
		s.t.Fatal(err)
	}
	if err := os.Rename(target+".next", target); err != nil {
		s.t.Fatal(err)
	}
}
func (s *SSH) MachineCommand(remote *Stack, name, body string) {
	s.t.Helper()
	if err := os.WriteFile(filepath.Join(remote.Dir, "machine-bin", name), []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
		s.t.Fatal(err)
	}
}

func (s *SSH) StartMaster(home *Stack, host, destination string) {
	s.t.Helper()
	path := filepath.Join(s.Dir, "masters", "default-"+strings.ReplaceAll(host, "-", "")[:12]+".sock")
	result := home.Run(Invocation{Binary: filepath.Join(home.Dir, "ssh-bin", "ssh"), Args: []string{"-o", "ControlPath=" + path, "-o", "ControlMaster=yes", "-N", "-T", destination}})
	if result.Code != 0 {
		s.t.Fatalf("start fake master: %+v", result)
	}
}

func (s *SSH) AwaitPausedRequest(destination string) {
	s.t.Helper()
	s.mu.Lock()
	machine := s.machines[destination]
	s.mu.Unlock()
	machine.link.mu.Lock()
	entered := machine.link.gate.entered
	machine.link.mu.Unlock()
	select {
	case <-entered:
	case <-time.After(fakeagent.HangGuard):
		s.t.Fatalf("no request reached paused SSH machine %s within %s", destination, fakeagent.HangGuard)
	}
}
func (s *SSH) Drain(destination string) {
	s.t.Helper()
	s.mu.Lock()
	machine := s.machines[destination]
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { machine.drain(); close(done) }()
	select {
	case <-done:
	case <-time.After(fakeagent.HangGuard):
		s.t.Fatalf("SSH machine %s did not drain within %s", destination, fakeagent.HangGuard)
	}
}
