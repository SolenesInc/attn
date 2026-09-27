package daemon

import (
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/store"
)

type appTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func newAppTestClock(d *Daemon) *appTestClock {
	clock := &appTestClock{now: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)}
	d.appClock = clock.Now
	return clock
}

func (c *appTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *appTestClock) advance(by time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(by)
	c.mu.Unlock()
}

func appEnabled(t *testing.T, d *Daemon, name string) bool {
	t.Helper()
	consumer, ok, err := d.store.GetBusConsumer(apps.ConsumerName(name))
	if err != nil || !ok {
		t.Fatalf("consumer for %s: %v ok=%t", name, err, ok)
	}
	return consumer.Enabled
}

func appNotifications(t *testing.T, d *Daemon, kind string) []store.NotificationRecord {
	t.Helper()
	all, err := d.store.ListNotifications()
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	var out []store.NotificationRecord
	for _, record := range all {
		if record.Kind == kind {
			out = append(out, record)
		}
	}
	return out
}
