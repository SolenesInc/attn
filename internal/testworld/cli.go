package testworld

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
)

type Invocation struct {
	Args    []string
	Session string
	Stdin   string
	Env     []string
	Binary  string
}

type Result struct {
	Stdout, Stderr string
	Code           int
}

func (r Result) JSON(t testing.TB, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(r.Stdout), v); err != nil {
		t.Fatalf("decode stdout as %T: %v\nstdout:\n%s\nstderr:\n%s", v, err, r.Stdout, r.Stderr)
	}
}

func (s *Stack) Attn(args ...string) Result {
	s.T.Helper()
	return s.Run(Invocation{Args: args})
}

func (s *Stack) Run(inv Invocation) Result {
	s.T.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	cmd := s.command(ctx, inv)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		s.T.Fatalf("attn %q still running after %s\nstdout:\n%s\nstderr:\n%s", inv.Args, fakeagent.HangGuard, stdout.String(), stderr.String())
	}
	return Result{Stdout: stdout.String(), Stderr: stderr.String(), Code: exitCode(s.T, inv, err)}
}

func (s *Stack) Launch(inv Invocation) *Running {
	s.T.Helper()
	r := &Running{t: s.T, args: inv.Args, grew: make(chan struct{}), done: make(chan struct{})}
	cmd := s.command(context.Background(), inv)
	cmd.Stdout = streamWriter{r, &r.stdout}
	cmd.Stderr = streamWriter{r, &r.stderr}
	if err := cmd.Start(); err != nil {
		s.T.Fatalf("start attn %q: %v", inv.Args, err)
	}
	r.process = cmd.Process
	go func() {
		err := cmd.Wait()
		r.mu.Lock()
		r.code = exitCode(s.T, inv, err)
		r.mu.Unlock()
		close(r.done)
	}()
	s.T.Cleanup(func() {
		select {
		case <-r.done:
		default:
			r.interrupt()
		}
	})
	return r
}

func (s *Stack) command(ctx context.Context, inv Invocation) *exec.Cmd {
	s.T.Helper()
	if len(inv.Args) > 0 && inv.Args[0] == "instance" {
		s.T.Fatal("attn instance resolves paths under $HOME and skips the routing fence, so a stack never runs it")
	}
	binary := inv.Binary
	if binary == "" {
		binary = s.binary
	}
	cmd := exec.CommandContext(ctx, binary, inv.Args...)
	cmd.WaitDelay = fakeagent.HangGuard
	cmd.Env = s.env()
	if inv.Session != "" {
		cmd.Env = append(cmd.Env, "ATTN_SESSION_ID="+inv.Session, "ATTN_INSIDE_APP=1")
	}
	cmd.Env = append(cmd.Env, inv.Env...)
	if inv.Stdin != "" {
		cmd.Stdin = strings.NewReader(inv.Stdin)
	}
	return cmd
}

func exitCode(t testing.TB, inv Invocation, err error) int {
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitCode()
	default:
		t.Errorf("run attn %q: %v", inv.Args, err)
		return -1
	}
}

type Running struct {
	t       testing.TB
	args    []string
	process *os.Process
	mu      sync.Mutex
	grew    chan struct{}
	done    chan struct{}
	stdout  bytes.Buffer
	stderr  bytes.Buffer
	code    int
}

func (r *Running) Wait() Result {
	r.t.Helper()
	select {
	case <-r.done:
	case <-time.After(fakeagent.HangGuard):
		r.mu.Lock()
		stderr := r.stderr.String()
		r.mu.Unlock()
		r.t.Fatalf("attn %q still running after %s\nstderr:\n%s", r.args, fakeagent.HangGuard, stderr)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return Result{Stdout: r.stdout.String(), Stderr: r.stderr.String(), Code: r.code}
}

type streamWriter struct {
	r      *Running
	stream *bytes.Buffer
}

func (w streamWriter) Write(p []byte) (int, error) {
	w.r.mu.Lock()
	defer w.r.mu.Unlock()
	w.stream.Write(p)
	close(w.r.grew)
	w.r.grew = make(chan struct{})
	return len(p), nil
}

func (r *Running) AwaitStdout(text string) {
	r.t.Helper()
	r.await("stdout", &r.stdout, text)
}

func (r *Running) AwaitStderr(text string) {
	r.t.Helper()
	r.await("stderr", &r.stderr, text)
}

func (r *Running) await(name string, stream *bytes.Buffer, text string) {
	r.t.Helper()
	deadline := time.After(fakeagent.HangGuard)
	for {
		r.mu.Lock()
		seen, grew := stream.String(), r.grew
		r.mu.Unlock()
		if strings.Contains(seen, text) {
			return
		}
		select {
		case <-grew:
		case <-r.done:
			r.mu.Lock()
			seen = stream.String()
			r.mu.Unlock()
			if !strings.Contains(seen, text) {
				r.t.Fatalf("attn %q exited %d without writing %q to %s:\n%s", r.args, r.code, text, name, seen)
			}
			return
		case <-deadline:
			r.t.Fatalf("attn %q wrote no %q to %s within %s:\n%s", r.args, text, name, fakeagent.HangGuard, seen)
		}
	}
}

func (r *Running) interrupt() {
	r.t.Helper()
	_ = r.process.Signal(syscall.SIGINT)
	select {
	case <-r.done:
	case <-time.After(fakeagent.HangGuard):
		r.t.Errorf("attn %q (pid %d) outlived SIGINT by %s", r.args, r.process.Pid, fakeagent.HangGuard)
		_ = r.process.Kill()
		<-r.done
	}
}
