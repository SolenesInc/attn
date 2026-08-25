package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestAssistantWindowFactProjectsOneSessionInvalidation(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopEventBus)
	trace := wireRecorder(d)

	d.publishFact(FactSessionAssistantWindowChanged, "sess-1", nil)

	if got := trace.EventNames(); len(got) != 1 || got[0] != protocol.EventSessionMessagesChanged {
		t.Fatalf("wire events = %v, want one %s", got, protocol.EventSessionMessagesChanged)
	}
	var message protocol.SessionMessagesChangedMessage
	if err := json.Unmarshal(trace.Payloads()[0], &message); err != nil {
		t.Fatalf("decode invalidation: %v", err)
	}
	if message.SessionID != "sess-1" {
		t.Fatalf("session_id = %q, want sess-1", message.SessionID)
	}

	logged, err := d.store.BusEventsSince(0, 10)
	if err != nil {
		t.Fatalf("BusEventsSince: %v", err)
	}
	if len(logged) != 1 || logged[0].Name != FactSessionAssistantWindowChanged || logged[0].Subject != "sess-1" {
		t.Fatalf("expected one subject-carrying assistant-window fact, got %+v", logged)
	}
}

// These run against the REAL sqlBusStore, not the in-memory fake internal/bus uses, so the SQLite adapter's semantics are what is under test.

// Inside a bubble the bus's safety-net poll is the slowest thing that can still deliver, so a condition still false after it is false for good.
func requireBus(t *testing.T, what string, cond func() bool) {
	t.Helper()
	time.Sleep(2 * bus.DefaultPollInterval)
	synctest.Wait()
	if !cond() {
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestGardenMutationPublishesAFactAndPushesTheGardenOnce(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopEventBus)

	var pushes int
	d.gardenBroadcastHook = func([]protocol.Seed, int) { pushes++ }

	d.publishFact(FactGardenNoted, "s-1", nil)

	if pushes != 1 {
		t.Fatalf("one fact produced %d garden pushes, want exactly 1", pushes)
	}

	events, err := d.store.BusEventsSince(0, 10)
	if err != nil {
		t.Fatalf("BusEventsSince: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("log holds %d events, want 1", len(events))
	}
	if events[0].Name != FactGardenNoted {
		t.Fatalf("fact name = %q, want %q", events[0].Name, FactGardenNoted)
	}
	if events[0].Subject != "s-1" {
		t.Fatalf("fact subject = %q, want the seed id", events[0].Subject)
	}
}

func TestSessionStateFactProjectsTheSameWireEvent(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopEventBus)

	d.store.Add(&protocol.Session{ID: "sess-1", Directory: "/tmp/x", State: "idle"})

	var events []string
	d.wsHub.broadcastListener = func(e *protocol.WebSocketEvent) { events = append(events, e.Event) }

	d.broadcastSessionStateChanged("sess-1")

	found := false
	for _, name := range events {
		if name == protocol.EventSessionStateChanged {
			found = true
		}
	}
	if !found {
		t.Fatalf("no session_state_changed on the wire; got %v", events)
	}

	logged, err := d.store.BusEventsSince(0, 10)
	if err != nil {
		t.Fatalf("BusEventsSince: %v", err)
	}
	if len(logged) != 1 || logged[0].Name != FactSessionStateChanged || logged[0].Subject != "sess-1" {
		t.Fatalf("expected one session.state.changed fact for sess-1, got %+v", logged)
	}
}

func TestActivityFactProjectsTheSessionSnapshot(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopEventBus)

	d.store.Add(&protocol.Session{ID: "sess-1", Directory: "/tmp/x", State: "working"})
	d.store.UpdateSessionActivity("sess-1", "running the frontend test suite", time.Now(), "v1:abc:512:0")

	var pushed []*protocol.WebSocketEvent
	d.wsHub.broadcastListener = func(e *protocol.WebSocketEvent) { pushed = append(pushed, e) }

	d.publishFact(FactSessionActivityChanged, "sess-1", nil)

	carried := false
	for _, event := range pushed {
		if event.Event != protocol.EventSessionStateChanged || event.Session == nil {
			continue
		}
		if event.Session.ID == "sess-1" && protocol.Deref(event.Session.Activity) == "running the frontend test suite" {
			carried = true
		}
	}
	if !carried {
		t.Fatalf("no pushed snapshot carried the new activity line; got %+v", pushed)
	}

	logged, err := d.store.BusEventsSince(0, 10)
	if err != nil {
		t.Fatalf("BusEventsSince: %v", err)
	}
	if len(logged) != 1 || logged[0].Name != FactSessionActivityChanged || logged[0].Subject != "sess-1" {
		t.Fatalf("expected one session.activity.changed fact for sess-1, got %+v", logged)
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

func TestBusStatusReportsLag(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopEventBus)

	if err := d.store.SaveBusConsumer(store.BusConsumer{
		Name: "watcher", Cursor: 1, Filter: "ticket.*", Enabled: true,
	}, time.Now()); err != nil {
		t.Fatalf("SaveBusConsumer: %v", err)
	}
	for i := 0; i < 3; i++ {
		d.publishTicketFact(FactTicketChanged, "tk-1")
	}

	st, err := d.BusStatus()
	if err != nil {
		t.Fatalf("BusStatus: %v", err)
	}
	if st.Head != 3 {
		t.Fatalf("head = %d, want 3", st.Head)
	}
	if len(st.Consumers) != 1 {
		t.Fatalf("consumers = %+v, want one", st.Consumers)
	}
	if st.Consumers[0].Lag != 2 {
		t.Fatalf("lag = %d, want 2", st.Consumers[0].Lag)
	}
	if st.Consumers[0].Live {
		t.Fatal("a consumer with no delivery loop reported Live")
	}
}

type failingAppendBusStore struct {
	bus.Store
	mu   sync.Mutex
	fail error
}

func (f *failingAppendBusStore) Append(e bus.Event, now time.Time) (int64, error) {
	f.mu.Lock()
	err := f.fail
	f.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return f.Store.Append(e, now)
}

func (f *failingAppendBusStore) setFail(err error) {
	f.mu.Lock()
	f.fail = err
	f.mu.Unlock()
}

func rewireBusWithFailingAppend(t *testing.T, d *Daemon) *failingAppendBusStore {
	t.Helper()
	d.stopEventBus()
	backing := &failingAppendBusStore{Store: d.newSQLBusStore()}
	d.eventBus = bus.New(bus.Options{Store: backing, Log: d.logf})
	d.busUnsubscribe = d.eventBus.Subscribe(bus.All, d.projectToClients)
	t.Cleanup(d.stopEventBus)
	return backing
}

func TestSnapshotStillPushesWhenTheBusAppendFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backing := rewireBusWithFailingAppend(t, d)

	var pushes int
	d.gardenBroadcastHook = func([]protocol.Seed, int) { pushes++ }

	backing.setFail(errors.New("disk had a bad night"))
	d.publishFact(FactGardenNoted, "s-1", nil)

	if pushes != 1 {
		t.Fatalf("a committed mutation produced %d pushes while the bus append was failing, want 1", pushes)
	}
	logged, err := d.store.BusEventsSince(0, 10)
	if err != nil {
		t.Fatalf("BusEventsSince: %v", err)
	}
	if len(logged) != 0 {
		t.Fatalf("append was supposed to fail, but the log holds %d event(s)", len(logged))
	}

	backing.setFail(nil)
	d.publishFact(FactGardenNoted, "s-1", nil)
	if pushes != 2 {
		t.Fatalf("garden pushes = %d after recovery, want 2", pushes)
	}
	logged, err = d.store.BusEventsSince(0, 10)
	if err != nil {
		t.Fatalf("BusEventsSince: %v", err)
	}
	if len(logged) != 1 {
		t.Fatalf("log holds %d event(s) after recovery, want 1", len(logged))
	}
}

func TestSessionStateStillReachesTheWireWhenTheBusAppendFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backing := rewireBusWithFailingAppend(t, d)

	d.store.Add(&protocol.Session{ID: "sess-1", Directory: "/tmp/x", State: "idle"})

	var events []string
	d.wsHub.broadcastListener = func(e *protocol.WebSocketEvent) { events = append(events, e.Event) }

	backing.setFail(errors.New("disk had a bad night"))
	d.broadcastSessionStateChanged("sess-1")

	found := false
	for _, name := range events {
		if name == protocol.EventSessionStateChanged {
			found = true
		}
	}
	if !found {
		t.Fatalf("a failing bus append silenced session_state_changed; wire saw %v", events)
	}
}
