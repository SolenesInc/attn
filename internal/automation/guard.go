package automation

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const guardTimeout = 60 * time.Second
const guardOutputLimit = 64 * 1024
const guardStderrTail = 2 * 1024

type GuardResult struct {
	Output  string
	Failure string
}

func RunGuard(ctx context.Context, dir string, env []string, command string) GuardResult {
	timeout := errors.New("guard timed out after 60s")
	ctx, stop := context.WithTimeoutCause(ctx, guardTimeout, timeout)
	defer stop()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	outputLimit := fmt.Errorf("guard printed more than %d bytes", guardOutputLimit)
	stdout := guardOutput{cancel: func() { cancel(outputLimit) }}
	stderr := guardTail{}
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := ctx.Err(); err != nil {
		return GuardResult{Failure: "guard cancelled: " + err.Error()}
	}
	if err := cmd.Start(); err != nil {
		return GuardResult{Failure: "guard start: " + err.Error()}
	}
	done := make(chan struct{})
	killed := make(chan struct{})
	go func() {
		defer close(killed)
		select {
		case <-ctx.Done():
		case <-done:
		}
		if ctx.Err() != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}()
	err := cmd.Wait()
	close(done)
	<-killed
	if cause := context.Cause(ctx); cause != nil {
		switch {
		case errors.Is(cause, timeout):
			return GuardResult{Failure: timeout.Error()}
		case errors.Is(cause, outputLimit):
			return GuardResult{Failure: outputLimit.Error()}
		default:
			return GuardResult{Failure: "guard cancelled: " + cause.Error()}
		}
	}
	if err != nil {
		detail := err.Error()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			detail = fmt.Sprintf("exit %d", exit.ExitCode())
		}
		if tail := strings.TrimSpace(string(stderr.data)); tail != "" {
			detail += ": " + tail
		}
		return GuardResult{Failure: detail}
	}
	return GuardResult{Output: strings.TrimSpace(string(stdout.data))}
}

type guardOutput struct {
	data   []byte
	cancel func()
}

func (w *guardOutput) Write(p []byte) (int, error) {
	available := guardOutputLimit - len(w.data)
	if len(p) > available {
		w.data = append(w.data, p[:available]...)
		w.cancel()
	} else {
		w.data = append(w.data, p...)
	}
	return len(p), nil
}

type guardTail struct{ data []byte }

func (w *guardTail) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) >= guardStderrTail {
		w.data = append(w.data[:0], p[len(p)-guardStderrTail:]...)
	} else {
		if excess := len(w.data) + len(p) - guardStderrTail; excess > 0 {
			w.data = w.data[excess:]
		}
		w.data = append(w.data, p...)
	}
	return n, nil
}
