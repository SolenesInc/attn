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
