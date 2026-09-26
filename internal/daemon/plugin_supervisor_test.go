package daemon

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/supervise"
)

func TestParkedPluginRaisesADurableNotification(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
	exitCode := 9
	d.notifyPluginParked("looper", pluginRuntimeSnapshot{
		Phase:          pluginPhaseParked,
		RestartAttempt: 10,
		LastExit:       &pluginExit{At: time.Now(), ExitCode: &exitCode},
	})

	list, err := d.store.ListNotifications()
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("notifications=%d, want 1", len(list))
	}
	record := list[0]
	if record.Kind != notificationKindPluginParked || record.SourceKind != "plugin" || record.SourceID != "looper" {
		t.Fatalf("notification=%+v, want a plugin-parked record for looper", record)
	}
	if !strings.Contains(record.Title, "looper") || !strings.Contains(record.Body, "10") {
		t.Fatalf("notification title=%q body=%q, want the plugin name and the restart count", record.Title, record.Body)
	}
	if !strings.Contains(record.Detail, "exit code 9") {
		t.Fatalf("notification detail=%q, want the last exit", record.Detail)
	}
	if record.Severity != store.NotificationCritical {
		t.Fatalf("severity = %q, want critical", record.Severity)
	}
}

func newTestPluginSupervisor(t *testing.T, clock *fakePluginClock, launcher *fakePluginLauncher) *pluginSupervisor {
	t.Helper()
	supervisor := newPluginSupervisor(launcher, clock, func(manifest pluginManifest, generation uint64) []string {
		return []string{fmt.Sprintf("ATTN_PLUGIN_NAME=%s", manifest.Name), fmt.Sprintf("ATTN_PLUGIN_GENERATION=%d", generation)}
	}, supervise.Options{})
	t.Cleanup(supervisor.Shutdown)
	return supervisor
}

type fakePluginLauncher struct {
	mu          sync.Mutex
	handles     []*fakePluginProcess
	startErrors []error
	envs        [][]string
}

func (l *fakePluginLauncher) Start(_ pluginManifest, env []string, _ io.Writer) (pluginProcessHandle, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.envs = append(l.envs, append([]string(nil), env...))
	index := len(l.envs) - 1
	if index < len(l.startErrors) && l.startErrors[index] != nil {
		return nil, l.startErrors[index]
	}
	handle := &fakePluginProcess{wait: make(chan pluginExit, 1)}
	l.handles = append(l.handles, handle)
	return handle, nil
}

func (l *fakePluginLauncher) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.envs)
}

func (l *fakePluginLauncher) handle(index int) *fakePluginProcess {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.handles[index]
}

type fakePluginProcess struct {
	mu     sync.Mutex
	wait   chan pluginExit
	exited bool
	kills  int
}

func (p *fakePluginProcess) Wait() pluginExit { return <-p.wait }

func (p *fakePluginProcess) Kill() error {
	p.mu.Lock()
	p.kills++
	alreadyExited := p.exited
	if !alreadyExited {
		p.exited = true
	}
	p.mu.Unlock()
	if !alreadyExited {
		p.wait <- pluginExit{Signal: "killed"}
	}
	return nil
}

func (p *fakePluginProcess) exit(exit pluginExit) {
	p.mu.Lock()
	if p.exited {
		p.mu.Unlock()
		return
	}
	p.exited = true
	p.mu.Unlock()
	p.wait <- exit
}

type fakePluginClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakePluginTimer
}

func newFakePluginClock() *fakePluginClock {
	return &fakePluginClock{now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)}
}

func (c *fakePluginClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakePluginClock) AfterFunc(delay time.Duration, fn func()) pluginSupervisorTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakePluginTimer{clock: c, at: c.now.Add(delay), fn: fn}
	c.timers = append(c.timers, timer)
	return timer
}

func (c *fakePluginClock) Advance(delay time.Duration) {
	target := c.Now().Add(delay)
	for {
		c.mu.Lock()
		var next *fakePluginTimer
		for _, timer := range c.timers {
			if timer.stopped || timer.fired || timer.at.After(target) {
				continue
			}
			if next == nil || timer.at.Before(next.at) {
				next = timer
			}
		}
		if next == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		c.now = next.at
		next.fired = true
		fn := next.fn
		c.mu.Unlock()
		fn()
	}
}

type fakePluginTimer struct {
	clock   *fakePluginClock
	at      time.Time
	fn      func()
	stopped bool
	fired   bool
}

func (t *fakePluginTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	return true
}

func requireSupervisor(t *testing.T, condition func() bool, what string) {
	t.Helper()
	synctest.Wait()
	if !condition() {
		t.Fatal(what)
	}
}

func waitForSupervisor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("supervisor condition did not become true")
}

func intPtr(value int) *int { return &value }
