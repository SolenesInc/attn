package bus

import (
	"context"
	"testing"
	"time"
)

func setEnabled(s *memStore, enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.consumers["c"]
	c.Enabled = enabled
	s.consumers["c"] = c
}

// runConsumer starts a bus with consumer "c" on a.* and publishes a.one,
// a.two and skipped.three while the first handler call is held open.
// onFirst runs inside that first call; later calls are recorded on handled.
func runConsumer(t *testing.T, s *memStore, onFirst func()) (handled chan string) {
	t.Helper()
	b := New(Options{
		Store: s, PollInterval: time.Millisecond,
		Now: func() time.Time { return statusNow },
	})
	t.Cleanup(b.Stop)
	handled = make(chan string, 8)
	release := make(chan struct{})
	first := true
	if err := b.Register("c", ParseFilter("a.*"), func(_ context.Context, ev Event) error {
		handled <- ev.Name
		if first {
			first = false
			<-release
			onFirst()
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Publish("a.one", "x", nil); err != nil {
		t.Fatal(err)
	}
	<-handled
	handled <- "a.one"
	for _, name := range []string{"a.two", "skipped.three"} {
		if _, err := b.Publish(name, "x", nil); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	return handled
}

func waitAttempt(t *testing.T, s *memStore, cursor int64, applied bool) {
	t.Helper()
	for a := range s.attempts {
		if a.cursor == cursor && a.applied == applied {
			return
		}
	}
}

func cursorOf(s *memStore) int64 {
	c, _, _ := s.GetConsumer("c")
	return c.Cursor
}

// A disable that lands while a handler runs is final: the drain has not
// polled yet, but no later cursor write may land. The in-flight event is
// redelivered on re-enable (durable handlers are idempotent).
func TestADisableDuringAnEventFreezesTheCursorAndReEnableRedeliversIt(t *testing.T) {
	s := newMemStore()
	s.attempts = make(chan cursorAttempt, 16)
	handled := runConsumer(t, s, func() { setEnabled(s, false) })

	waitAttempt(t, s, 1, false)
	if got := cursorOf(s); got != 0 {
		t.Fatalf("disabled consumer's cursor moved to %d; want 0", got)
	}
	setEnabled(s, true)
	waitAttempt(t, s, 3, true)

	var got []string
	for len(handled) > 0 {
		got = append(got, <-handled)
	}
	if want := []string{"a.one", "a.one", "a.two"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("handled %v; want %v", got, want)
	}
	if got := cursorOf(s); got != 3 {
		t.Errorf("cursor %d after re-enable; want 3", got)
	}
}

// Disable then enable before the running drain writes: the write applies
// again and is the next event past the cursor, so nothing moves back or is
// skipped.
func TestADisableAndEnableInsideOneEventKeepsTheCursorMovingForward(t *testing.T) {
	s := newMemStore()
	s.attempts = make(chan cursorAttempt, 16)
	handled := runConsumer(t, s, func() { setEnabled(s, false); setEnabled(s, true) })

	waitAttempt(t, s, 3, true)
	var got []string
	for len(handled) > 0 {
		got = append(got, <-handled)
	}
	if len(got) != 2 || got[0] != "a.one" || got[1] != "a.two" {
		t.Errorf("handled %v; want [a.one a.two]", got)
	}
}
