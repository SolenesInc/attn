package bus

import (
	"context"
	"testing"
	"time"
)

// A disable that lands while a handler runs is final: the drain's own poll
// has not noticed it yet, but no later cursor write may land.
func TestADisableDuringAnEventFreezesTheCursor(t *testing.T) {
	s := newMemStore()
	b := New(Options{Store: s, Now: func() time.Time { return statusNow }})
	for _, name := range []string{"a.one", "a.two", "skipped.three"} {
		if _, err := s.Append(Event{Name: name, Subject: name, Source: "test"}, statusNow); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveConsumer(Consumer{Name: "c", Filter: "a.*", Enabled: true}, statusNow); err != nil {
		t.Fatal(err)
	}
	var handled []string
	d := b.newDurable("c", ParseFilter("a.*"), nil, func(_ context.Context, ev Event) error {
		handled = append(handled, ev.Name)
		s.mu.Lock()
		c := s.consumers["c"]
		c.Enabled = false
		s.consumers["c"] = c
		s.mu.Unlock()
		return nil
	})

	if err := b.drain(d); err != nil {
		t.Fatalf("drain after a disable: %v", err)
	}
	rec, _, _ := s.GetConsumer("c")
	if rec.Cursor != 0 {
		t.Errorf("disabled consumer's cursor moved to %d; want 0", rec.Cursor)
	}
	if len(handled) != 1 {
		t.Errorf("handled %v; want only the in-flight event", handled)
	}
}

func setEnabled(s *memStore, enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.consumers["c"]
	c.Enabled = enabled
	s.consumers["c"] = c
}

func seedDisableBus(t *testing.T) (*Bus, *memStore) {
	t.Helper()
	s := newMemStore()
	b := New(Options{Store: s, Now: func() time.Time { return statusNow }})
	for _, name := range []string{"a.one", "a.two", "skipped.three"} {
		if _, err := s.Append(Event{Name: name, Subject: name, Source: "test"}, statusNow); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveConsumer(Consumer{Name: "c", Filter: "a.*", Enabled: true}, statusNow); err != nil {
		t.Fatal(err)
	}
	return b, s
}

// Durable handlers are idempotent, so the unacked in-flight event may run
// twice on re-enable; the effect set must still be each event exactly once.
func TestReEnablingAfterADisableRedeliversTheUnackedEventAndSkipsNothing(t *testing.T) {
	b, s := seedDisableBus(t)
	effects := map[int64]bool{}
	first := true
	d := b.newDurable("c", ParseFilter("a.*"), nil, func(_ context.Context, ev Event) error {
		effects[ev.Seq] = true
		if first {
			first = false
			setEnabled(s, false)
		}
		return nil
	})
	if err := b.drain(d); err != nil {
		t.Fatal(err)
	}
	setEnabled(s, true)
	if err := b.drain(d); err != nil {
		t.Fatal(err)
	}
	rec, _, _ := s.GetConsumer("c")
	if rec.Cursor != 3 || len(effects) != 2 || !effects[1] || !effects[2] {
		t.Errorf("cursor %d, effects %v; want cursor 3 and events 1 and 2 applied", rec.Cursor, effects)
	}
}

// Disable then enable before the running drain reaches its next write: the
// write applies again, and it is the next event after the cursor, so the
// cursor never moves back and no event is skipped.
func TestADisableAndEnableInsideOneEventKeepsTheCursorMovingForward(t *testing.T) {
	b, s := seedDisableBus(t)
	var handled []int64
	first := true
	d := b.newDurable("c", ParseFilter("a.*"), nil, func(_ context.Context, ev Event) error {
		handled = append(handled, ev.Seq)
		if first {
			first = false
			setEnabled(s, false)
			setEnabled(s, true)
		}
		return nil
	})
	if err := b.drain(d); err != nil {
		t.Fatal(err)
	}
	rec, _, _ := s.GetConsumer("c")
	if rec.Cursor != 3 || len(handled) != 2 || handled[0] != 1 || handled[1] != 2 {
		t.Errorf("cursor %d, handled %v; want cursor 3 and events 1, 2 once each", rec.Cursor, handled)
	}
}
