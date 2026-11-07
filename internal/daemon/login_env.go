package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/supervise"
)

// A plugin that fails to connect restarts with backoff that reaches 61 s, the same time supervise needs to call a connection stable.
const pluginStartLimit = supervise.StableConnection

const PluginStartLimitTimer stepclock.Name = "plugin-start-limit"

// loginEnv owns the login environment. One capture runs at a time. A failed capture is not remembered: the next Wait starts
// another, and until one succeeds the latest answer stays the failed one, so a retry never holds a reader that does not launch.
type loginEnv struct {
	life *lifetime
	logf func(string, ...interface{})

	settled chan struct{}

	mu      sync.Mutex
	running *loginCapture
	latest  *loginCapture
}

type loginCapture struct {
	done  chan struct{}
	shell string
	env   []string
	err   error
}

func newLoginEnv(life *lifetime, logf func(string, ...interface{})) *loginEnv {
	return &loginEnv{life: life, logf: logf, settled: make(chan struct{}, 1)}
}

func (e *loginEnv) Begin() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running == nil && e.latest == nil {
		e.startLocked()
	}
}

func (e *loginEnv) startLocked() *loginCapture {
	c := &loginCapture{done: make(chan struct{})}
	e.running = c
	if !e.life.Go("captureLoginEnv", func() { e.capture(c) }) {
		e.settleLocked(c, e.life.Context().Err())
	}
	return c
}

func (e *loginEnv) capture(c *loginCapture) {
	c.shell = pty.GetUserLoginShell()
	var err error
	if c.shell != "" {
		c.env, err = pty.ReadLoginShellEnv(e.life.Context(), c.shell)
	}
	if err != nil {
		e.logf("login environment capture failed for %s: %v", c.shell, err)
	} else {
		e.logf("login environment captured: shell=%s vars=%d", c.shell, len(c.env))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.settleLocked(c, err)
}

func (e *loginEnv) settleLocked(c *loginCapture, err error) {
	c.err = err
	e.running, e.latest = nil, c
	close(c.done)
	select {
	case e.settled <- struct{}{}:
	default:
	}
}

// Wait is how something that will keep the environment for its life reads it: an agent or shell pane starting, or a shared server.
// It joins the running capture, or starts one when the latest failed.
func (e *loginEnv) Wait(ctx context.Context) ([]string, error) {
	e.mu.Lock()
	c := e.running
	if c == nil && (e.latest == nil || e.latest.err != nil) {
		c = e.startLocked()
	}
	if c == nil {
		c = e.latest
	}
	e.mu.Unlock()
	return c.await(ctx)
}

// Latest is how everything but a launch reads the environment: it waits only for the first capture, and starts none.
func (e *loginEnv) Latest(ctx context.Context) []string {
	e.Begin()
	e.mu.Lock()
	c := e.latest
	if c == nil {
		c = e.running
	}
	e.mu.Unlock()
	env, _ := c.await(ctx)
	return env
}

func (c *loginCapture) await(ctx context.Context) ([]string, error) {
	select {
	case <-c.done:
		return c.env, c.err
	default:
	}
	select {
	case <-c.done:
		return c.env, c.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (e *loginEnv) answered() *loginCapture {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.latest
}

func (d *Daemon) launchEnv(ctx context.Context) ([]string, error) {
	env, err := d.loginEnv.Wait(ctx)
	if err != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("stopped waiting for the login environment: %w", err)
	}
	return env, nil
}

func (d *Daemon) latestLoginEnv() []string {
	return d.loginEnv.Latest(d.life.Context())
}

func (d *Daemon) capturingLoginEnv() bool {
	return d.loginEnv.answered() == nil
}

func (d *Daemon) loginEnvFailed() bool {
	latest := d.loginEnv.answered()
	return latest != nil && latest.err != nil
}

func (d *Daemon) followLoginEnv() {
	pluginsStarted := false
	shownFailure := d.loginEnvFailureStillUnread()
	for {
		select {
		case <-d.loginEnv.settled:
		case <-d.life.Done():
			return
		}
		if d.stopping() {
			return
		}
		shownFailure = d.showLoginEnvFailure(d.loginEnv.answered(), shownFailure)
		d.ensureAvailableAgentSkills()
		d.publishHarnessAvailability()
		if !pluginsStarted {
			pluginsStarted = true
			d.life.Go("startPluginsAfterLoginEnvCapture", d.startPluginsAfterLoginEnvCapture)
		}
	}
}

func (d *Daemon) loginEnvFailureStillUnread() string {
	existing, err := d.store.ListNotifications("")
	if err != nil {
		d.logf("notifications: list before following the login environment: %v", err)
		return ""
	}
	for _, n := range existing {
		if n.Kind == notificationKindLoginEnvFailed && n.ReadAt.IsZero() {
			return n.Cause
		}
	}
	return ""
}

func (d *Daemon) startPluginsAfterLoginEnvCapture() {
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

const notificationKindLoginEnvFailed = "login_env_failed"

// showLoginEnvFailure keeps one unread notification for the failure shown, and none once a capture succeeds.
func (d *Daemon) showLoginEnvFailure(latest *loginCapture, shown string) string {
	failure := ""
	if latest.err != nil {
		failure = fmt.Sprintf("%s did not give attn its environment within the %s budget: %v.", latest.shell, pty.ShellEnvTimeout, latest.err)
	}
	if failure == shown {
		return shown
	}
	existing, err := d.store.ListNotifications("")
	if err != nil {
		d.logf("notifications: list before showing the login environment failure: %v", err)
		return shown
	}
	for _, n := range existing {
		if n.Kind != notificationKindLoginEnvFailed {
			continue
		}
		resolved, err := d.store.ResolveNotification(n.ID, time.Now())
		if err != nil {
			d.logf("notifications: resolve the login environment failure: %v", err)
			continue
		}
		if resolved {
			d.publishFact(FactNotificationRead, n.ID, nil)
		}
	}
	if failure == "" {
		return ""
	}
	impact := "Agents, shell panes and plugins started now do not get the variables and PATH from your shell's login files, and agents found only on that PATH read as still being checked. The next launch tries again, and this clears when a capture succeeds."
	record, err := d.store.AddNotification(store.NotificationRecord{
		Kind:       notificationKindLoginEnvFailed,
		Severity:   store.NotificationWarning,
		Title:      "attn could not read your login shell's environment",
		Body:       failure + " " + impact,
		Trigger:    "attn ran your login shell to read its environment.",
		Impact:     impact,
		Cause:      failure,
		SourceKind: "login_shell",
		SourceID:   latest.shell,
	}, time.Now())
	if err != nil {
		d.logf("notifications: add the login environment failure: %v", err)
		return shown
	}
	d.publishFact(FactNotificationCreated, record.ID, nil)
	return failure
}

// awaitHarness holds a launch until the daemon can answer whether its harness exists: until the first capture ends, and again
// when only a failed capture stands between the launch and its harness. The terminal start runs the capture for a launch that
// found its harness, so one launch waits for one capture. For a harness
// no plugin has registered yet, until a plugin registers it or the plugins started after the first capture have had pluginStartLimit.
// The caller then looks the harness up as usual.
func (d *Daemon) awaitHarness(agent string) {
	agent = strings.TrimSpace(strings.ToLower(agent))
	if agent == "" || agent == protocol.AgentShellValue {
		return
	}
	d.latestLoginEnv()
	if driver := agentdriver.Get(agent); driver != nil {
		if _, err := d.machineAgentExecutable(driver, ""); err != nil && d.loginEnvFailed() {
			_, _ = d.launchEnv(d.life.Context())
		}
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
