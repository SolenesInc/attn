package daemon

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/bus"
)

func requireBus(t *testing.T, what string, cond func() bool) {
	t.Helper()
	time.Sleep(2 * bus.DefaultPollInterval)
	synctest.Wait()
	if !cond() {
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestDurableConsumerCatchesUpOverTheSQLiteAdapter(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		backing := d.newSQLBusStore()

		var (
			mu   sync.Mutex
			seen []string
		)
		record := func(_ context.Context, ev bus.Event) error {
			mu.Lock()
			seen = append(seen, ev.Name+":"+ev.Subject)
			mu.Unlock()
			return nil
		}
		snapshot := func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), seen...)
		}

		first := bus.New(bus.Options{Store: backing})
		if err := first.Register("ticket-watcher", bus.Filter{"ticket.*"}, record); err != nil {
			t.Fatalf("Register: %v", err)
		}
		if err := first.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if _, err := first.Publish(FactTicketCreated, "tk-1", nil); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		requireBus(t, "the first delivery", func() bool { return len(snapshot()) == 1 })
		first.Stop()

		offline := bus.New(bus.Options{Store: backing})
		for _, fact := range []struct{ name, subject string }{
			{FactTicketCommented, "tk-1"},
			{FactSessionStateChanged, "sess-9"},
			{FactTicketStatusChanged, "tk-1"},
		} {
			if _, err := offline.Publish(fact.name, fact.subject, nil); err != nil {
				t.Fatalf("Publish(%s): %v", fact.name, err)
			}
		}

		second := bus.New(bus.Options{Store: backing})
		if err := second.Register("ticket-watcher", bus.Filter{"ticket.*"}, record); err != nil {
			t.Fatalf("Register: %v", err)
		}
		if err := second.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Cleanup(second.Stop)

		requireBus(t, "catch-up", func() bool { return len(snapshot()) == 3 })
		got := snapshot()
		want := []string{
			FactTicketCreated + ":tk-1",
			FactTicketCommented + ":tk-1",
			FactTicketStatusChanged + ":tk-1",
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("delivered %v, want %v", got, want)
			}
		}

		rec, ok, err := d.store.GetBusConsumer("ticket-watcher")
		if err != nil || !ok {
			t.Fatalf("GetBusConsumer: %v (found=%v)", err, ok)
		}
		if rec.Cursor != 4 {
			t.Fatalf("persisted cursor is %d, want 4 (head)", rec.Cursor)
		}
		if rec.Filter != "ticket.*" {
			t.Fatalf("persisted filter is %q", rec.Filter)
		}
	})
}
