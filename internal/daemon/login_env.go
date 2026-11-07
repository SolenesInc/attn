package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/supervise"
)

// A plugin that fails to connect restarts with backoff that reaches 61 s, the same time supervise needs to call a connection stable.
const pluginStartLimit = supervise.StableConnection

const PluginStartLimitTimer stepclock.Name = "plugin-start-limit"

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

func (d *Daemon) capturingLoginEnv() bool {
	_, capturing := d.loginEnv.Peek()
	return capturing
}

func (d *Daemon) finishLoginEnvCapture() {
	_, _ = d.loginEnv.Wait(d.life.Context())
	if d.stopping() {
		return
	}
	d.ensureAvailableAgentSkills()
	d.publishHarnessAvailability()
	if d.startDiscoveredPlugins() == 0 {
		close(d.pluginStartEnded)
		return
	}
	limit := d.clock.NewTimer(PluginStartLimitTimer, pluginStartLimit)
	defer limit.Stop()
	select {
	case <-limit.C():
		close(d.pluginStartEnded)
	case <-d.life.Done():
	}
}

// awaitHarness holds a launch until the daemon can answer whether its harness exists: until the capture ends, and for a harness
// no plugin has registered yet, until a plugin registers it or the plugins started after the capture have had pluginStartLimit.
// The caller then looks the harness up as usual.
func (d *Daemon) awaitHarness(agent string) {
	agent = strings.TrimSpace(strings.ToLower(agent))
	if agent == "" || agent == protocol.AgentShellValue {
		return
	}
	d.launchEnvUntilStop()
	if agentdriver.Get(agent) != nil {
		return
	}
	for {
		registered, next := d.ensurePluginRegistry().driverOrRegistration(agent)
		if registered {
			return
		}
		pausepoint.At(pausepoint.LaunchAwaitsPluginHarness)
		select {
		case <-next:
		case <-d.pluginStartEnded:
			d.logf("harness %q: no plugin registered it within %s of the login environment capture", agent, pluginStartLimit)
			return
		case <-d.life.Done():
			return
		}
	}
}

func (d *Daemon) publishHarnessAvailability() {
	d.coalesceSnapshots(func() {
		for _, name := range agentdriver.List() {
			d.publishFact(FactSettingChanged, availabilitySettingKey(name), nil)
		}
	})
}
