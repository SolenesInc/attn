package testworld

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/stepclock"
)

func listenForClock(t testing.TB, dir string) *stepclock.Hub {
	t.Helper()
	hub, err := stepclock.Listen(filepath.Join(dir, "clock.sock"))
	if err != nil {
		t.Fatalf("listen for stepped clock: %v", err)
	}
	t.Cleanup(hub.Close)
	return hub
}

// Advance moves the stepped clock of the daemon and every process it started.
func (s *Stack) Advance(d time.Duration) {
	s.T.Helper()
	advanceHub(s.T, s.clock, d)
}

// AwaitTimer waits until the daemon or a process it started has a timer of d pending, armed after the last Advance or AwaitTimer.
func (s *Stack) AwaitTimer(d time.Duration) {
	s.T.Helper()
	awaitHubTimer(s.T, s.clock, d)
}

// ChildClock is the stepped clock of the processes a test starts itself, such as PTY workers.
// The test process keeps real time.
type ChildClock struct {
	t   testing.TB
	hub *stepclock.Hub
}

func StepChildren(t *testing.T) *ChildClock {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "attn-clock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	hub := listenForClock(t, dir)
	key, value, _ := strings.Cut(hub.Env(), "=")
	t.Setenv(key, value)
	return &ChildClock{t: t, hub: hub}
}

func (c *ChildClock) Advance(d time.Duration) {
	c.t.Helper()
	advanceHub(c.t, c.hub, d)
}

func (c *ChildClock) AwaitTimer(d time.Duration) {
	c.t.Helper()
	awaitHubTimer(c.t, c.hub, d)
}

func advanceHub(t testing.TB, hub *stepclock.Hub, d time.Duration) {
	t.Helper()
	if d < 0 {
		t.Fatalf("Advance duration %s is negative", d)
	}
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	if err := hub.Advance(d, guard.C); err != nil {
		t.Fatalf("Advance(%s): %v within %s", d, err, fakeagent.HangGuard)
	}
}

func awaitHubTimer(t testing.TB, hub *stepclock.Hub, d time.Duration) {
	t.Helper()
	if d < 0 {
		t.Fatalf("AwaitTimer duration %s is negative", d)
	}
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	if pending, ok := hub.AwaitTimer(d, guard.C); !ok {
		t.Fatalf("AwaitTimer(%s): no such timer armed within %s; timers pending: %v", d, fakeagent.HangGuard, pending)
	}
}
