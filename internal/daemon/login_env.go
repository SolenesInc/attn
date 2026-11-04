package daemon

import (
	"context"
	"fmt"
	"sync"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/pty"
)

// loginEnv owns the login environment: capturing while done is open, then ready, or failed when err is set.
type loginEnv struct {
	life *lifetime
	logf func(string, ...interface{})

	begin sync.Once
	done  chan struct{}
	env   []string
	err   error
}

func newLoginEnv(life *lifetime, logf func(string, ...interface{})) *loginEnv {
	return &loginEnv{life: life, logf: logf, done: make(chan struct{})}
}

func (e *loginEnv) Begin() {
	e.begin.Do(func() {
		if !e.life.Go("captureLoginEnv", e.capture) {
			e.settle(nil, e.life.Context().Err())
		}
	})
}

func (e *loginEnv) capture() {
	shell := pty.GetUserLoginShell()
	if shell == "" {
		e.settle(nil, nil)
		return
	}
	env, err := pty.ReadLoginShellEnv(e.life.Context(), shell)
	if err != nil {
		e.logf("login environment capture failed for %s: %v", shell, err)
		e.settle(nil, err)
		return
	}
	e.logf("login environment captured: shell=%s vars=%d", shell, len(env))
	e.settle(env, nil)
}

func (e *loginEnv) settle(env []string, err error) {
	e.env, e.err = env, err
	close(e.done)
}

func (e *loginEnv) Wait(ctx context.Context) ([]string, error) {
	e.Begin()
	if env, capturing := e.Peek(); !capturing {
		return env, e.err
	}
	select {
	case <-e.done:
		return e.env, e.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (e *loginEnv) Peek() (env []string, capturing bool) {
	select {
	case <-e.done:
		return e.env, false
	default:
		return nil, true
	}
}

func (d *Daemon) launchEnv(ctx context.Context) ([]string, error) {
	env, err := d.loginEnv.Wait(ctx)
	if err != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("stopped waiting for the login environment: %w", err)
	}
	return env, nil
}

func (d *Daemon) launchEnvUntilStop() []string {
	env, _ := d.launchEnv(d.life.Context())
	return env
}

func (d *Daemon) capturingInBackground() bool {
	if !d.backgroundLoginEnv {
		return false
	}
	_, capturing := d.loginEnv.Peek()
	return capturing
}

func (d *Daemon) finishLoginEnvCapture() {
	_, _ = d.loginEnv.Wait(d.life.Context())
	if d.stopping() {
		return
	}
	d.publishHarnessAvailability()
	d.ensureAvailableAgentSkills()
	d.startDiscoveredPlugins()
}

func (d *Daemon) publishHarnessAvailability() {
	d.coalesceSnapshots(func() {
		for _, name := range agentdriver.List() {
			d.publishFact(FactSettingChanged, availabilitySettingKey(name), nil)
		}
	})
}
