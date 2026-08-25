package store

import (
	"testing"
	"time"
)

var busBase = time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

func appendBus(t *testing.T, s *Store, name, subject string, at time.Time) int64 {
	t.Helper()
	seq, err := s.AppendBusEvent(BusEvent{Name: name, Subject: subject, Payload: `{"k":1}`, Source: "test"}, at)
	if err != nil {
		t.Fatalf("AppendBusEvent(%s): %v", name, err)
	}
	return seq
}

func TestBusEventLogIsOrderedAndReadableFromACursor(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	first := appendBus(t, s, "session.state.changed", "sess_1", busBase)
	second := appendBus(t, s, "ticket.commented", "tk_1", busBase.Add(time.Minute))
	third := appendBus(t, s, "session.state.changed", "sess_2", busBase.Add(2*time.Minute))

	if !(first < second && second < third) {
		t.Fatalf("seq not monotonic: %d, %d, %d", first, second, third)
	}

	events, err := s.BusEventsSince(first, 10)
	if err != nil {
		t.Fatalf("BusEventsSince: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events after seq %d, got %d", first, len(events))
	}
	if events[0].Seq != second || events[1].Seq != third {
		t.Fatalf("out of order: got %d, %d; want %d, %d", events[0].Seq, events[1].Seq, second, third)
	}
	if events[0].Name != "ticket.commented" || events[0].Subject != "tk_1" {
		t.Fatalf("payload columns not round-tripped: %+v", events[0])
	}
	if events[0].Payload != `{"k":1}` || events[0].Source != "test" {
		t.Fatalf("payload/source not round-tripped: %+v", events[0])
	}
	if !events[0].CreatedAt.Equal(busBase.Add(time.Minute)) {
		t.Fatalf("created_at not round-tripped: %v", events[0].CreatedAt)
	}
}

func TestBusEventsSinceRespectsLimit(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	for i := 0; i < 5; i++ {
		appendBus(t, s, "x.happened", "", busBase.Add(time.Duration(i)*time.Minute))
	}
	events, err := s.BusEventsSince(0, 2)
	if err != nil {
		t.Fatalf("BusEventsSince: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("limit ignored: got %d events, want 2", len(events))
	}
}

func TestBusBoundsTracksTheLiveWindow(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	earliest, head, err := s.BusBounds()
	if err != nil {
		t.Fatalf("BusBounds on empty log: %v", err)
	}
	if earliest != 0 || head != 0 {
		t.Fatalf("empty log should report zero bounds, got %d..%d", earliest, head)
	}

	lo := appendBus(t, s, "a.happened", "", busBase)
	hi := appendBus(t, s, "b.happened", "", busBase.Add(time.Minute))

	earliest, head, err = s.BusBounds()
	if err != nil {
		t.Fatalf("BusBounds: %v", err)
	}
	if earliest != lo || head != hi {
		t.Fatalf("bounds %d..%d, want %d..%d", earliest, head, lo, hi)
	}
}

func TestSetBusConsumerEnabledReportsAVanishedConsumer(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if err := s.SaveBusConsumer(BusConsumer{Name: "app:approval-gate", Cursor: 0, Filter: "*", Enabled: true}, busBase); err != nil {
		t.Fatalf("SaveBusConsumer: %v", err)
	}
	flipped, err := s.SetBusConsumerEnabled("app:approval-gate", false, busBase.Add(time.Minute))
	if err != nil {
		t.Fatalf("SetBusConsumerEnabled: %v", err)
	}
	if !flipped {
		t.Fatal("flipping a registered consumer reported no row")
	}

	if err := s.DeleteBusConsumer("app:approval-gate"); err != nil {
		t.Fatalf("DeleteBusConsumer: %v", err)
	}
	flipped, err = s.SetBusConsumerEnabled("app:approval-gate", true, busBase.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("SetBusConsumerEnabled after delete: %v", err)
	}
	if flipped {
		t.Fatal("flipping a consumer that no longer exists reported success")
	}
}

func TestSaveBusConsumerPreservesCursorAndEnabled(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if err := s.SaveBusConsumer(BusConsumer{Name: "wshub", Cursor: 0, Filter: "*", Enabled: true}, busBase); err != nil {
		t.Fatalf("SaveBusConsumer: %v", err)
	}
	if err := s.SetBusConsumerCursor("wshub", 42, busBase.Add(time.Minute)); err != nil {
		t.Fatalf("SetBusConsumerCursor: %v", err)
	}
	if _, err := s.SetBusConsumerEnabled("wshub", false, busBase.Add(2*time.Minute)); err != nil {
		t.Fatalf("SetBusConsumerEnabled: %v", err)
	}

	if err := s.SaveBusConsumer(BusConsumer{Name: "wshub", Cursor: 0, Filter: "session.*", Enabled: true}, busBase.Add(3*time.Minute)); err != nil {
		t.Fatalf("SaveBusConsumer (re-register): %v", err)
	}

	got, ok, err := s.GetBusConsumer("wshub")
	if err != nil || !ok {
		t.Fatalf("GetBusConsumer: %v (found=%v)", err, ok)
	}
	if got.Cursor != 42 {
		t.Fatalf("re-registration rewound the cursor to %d", got.Cursor)
	}
	if got.Enabled {
		t.Fatal("re-registration resurrected a killed consumer")
	}
	if got.Filter != "session.*" {
		t.Fatalf("filter not refreshed: %q", got.Filter)
	}
}

func TestDeleteBusConsumerReleasesTheRetentionFloor(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	appendBus(t, s, "a.happened", "", busBase)
	appendBus(t, s, "b.happened", "", busBase.Add(time.Minute))

	if err := s.SaveBusConsumer(BusConsumer{Name: "app:ghost", Cursor: 0, Filter: "*", Enabled: true}, busBase); err != nil {
		t.Fatalf("SaveBusConsumer: %v", err)
	}
	if n, err := s.TrimBusEvents(busBase.Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("trimmed %d event(s) (err=%v); an enabled row must pin the log", n, err)
	}

	if err := s.DeleteBusConsumer("app:ghost"); err != nil {
		t.Fatalf("DeleteBusConsumer: %v", err)
	}
	if _, ok, err := s.GetBusConsumer("app:ghost"); err != nil || ok {
		t.Fatalf("the row survived deletion (found=%v, err=%v)", ok, err)
	}
	if n, err := s.TrimBusEvents(busBase.Add(time.Hour)); err != nil || n != 2 {
		t.Fatalf("trimmed %d event(s) (err=%v) after the row went, want 2", n, err)
	}
}

func TestDeleteBusConsumerMissingIsSuccess(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if err := s.DeleteBusConsumer("nobody"); err != nil {
		t.Fatalf("DeleteBusConsumer of an unknown name: %v", err)
	}
}

func TestGetBusConsumerMissing(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	_, ok, err := s.GetBusConsumer("nobody")
	if err != nil {
		t.Fatalf("GetBusConsumer: %v", err)
	}
	if ok {
		t.Fatal("expected no registration")
	}
}

func TestListBusConsumers(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	for _, name := range []string{"zeta", "alpha"} {
		if err := s.SaveBusConsumer(BusConsumer{Name: name, Filter: "*", Enabled: true}, busBase); err != nil {
			t.Fatalf("SaveBusConsumer(%s): %v", name, err)
		}
	}
	rows, err := s.ListBusConsumers()
	if err != nil {
		t.Fatalf("ListBusConsumers: %v", err)
	}
	if len(rows) != 2 || rows[0].Name != "alpha" || rows[1].Name != "zeta" {
		t.Fatalf("expected alpha,zeta in name order, got %+v", rows)
	}
}

func TestTrimBusEventsHoldsTheLineForALaggingEnabledConsumer(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	old := busBase
	first := appendBus(t, s, "a.happened", "", old)
	second := appendBus(t, s, "b.happened", "", old.Add(time.Minute))
	appendBus(t, s, "c.happened", "", old.Add(2*time.Minute))

	if err := s.SaveBusConsumer(BusConsumer{Name: "slow", Cursor: first, Filter: "*", Enabled: true}, old); err != nil {
		t.Fatalf("SaveBusConsumer: %v", err)
	}

	n, err := s.TrimBusEvents(old.Add(time.Hour))
	if err != nil {
		t.Fatalf("TrimBusEvents: %v", err)
	}
	if n != 1 {
		t.Fatalf("trimmed %d events, want 1 (only what 'slow' has read)", n)
	}
	earliest, _, err := s.BusBounds()
	if err != nil {
		t.Fatalf("BusBounds: %v", err)
	}
	if earliest != second {
		t.Fatalf("earliest surviving seq is %d, want %d", earliest, second)
	}
}

func TestTrimBusEventsKeepsEventsInsideTheWindow(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	recent := appendBus(t, s, "a.happened", "", busBase)
	if err := s.SaveBusConsumer(BusConsumer{Name: "fast", Cursor: recent, Filter: "*", Enabled: true}, busBase); err != nil {
		t.Fatalf("SaveBusConsumer: %v", err)
	}

	n, err := s.TrimBusEvents(busBase.Add(-time.Hour))
	if err != nil {
		t.Fatalf("TrimBusEvents: %v", err)
	}
	if n != 0 {
		t.Fatalf("trimmed %d events inside the retention window, want 0", n)
	}
}

func TestTrimBusEventsKeepsDisabledInstalledAppBacklog(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	read := appendBus(t, s, "a.happened", "", busBase)
	unread := appendBus(t, s, "b.happened", "", busBase.Add(time.Minute))

	if err := s.SaveApp("history", busBase); err != nil {
		t.Fatalf("SaveApp: %v", err)
	}
	if err := s.SaveBusConsumer(BusConsumer{Name: "app:history", Cursor: read, Filter: "*", Enabled: false}, busBase); err != nil {
		t.Fatalf("SaveBusConsumer: %v", err)
	}

	n, err := s.TrimBusEvents(busBase.Add(time.Hour))
	if err != nil {
		t.Fatalf("TrimBusEvents: %v", err)
	}
	if n != 1 {
		t.Fatalf("trimmed %d events, want only the row the disabled app already read", n)
	}
	earliest, _, err := s.BusBounds()
	if err != nil || earliest != unread {
		t.Fatalf("earliest = %d, %v; want disabled app backlog at %d", earliest, err, unread)
	}
}

func TestTrimBusEventsIgnoresDisabledOrphanedAppConsumer(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	appendBus(t, s, "a.happened", "", busBase)
	appendBus(t, s, "b.happened", "", busBase.Add(time.Minute))
	if err := s.SaveBusConsumer(BusConsumer{Name: "app:removed", Cursor: 0, Filter: "*", Enabled: false}, busBase); err != nil {
		t.Fatalf("SaveBusConsumer: %v", err)
	}

	n, err := s.TrimBusEvents(busBase.Add(time.Hour))
	if err != nil {
		t.Fatalf("TrimBusEvents: %v", err)
	}
	if n != 2 {
		t.Fatalf("orphaned disabled row pinned the log: trimmed %d, want 2", n)
	}
}

func TestTrimBusEventsWithNoConsumers(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	appendBus(t, s, "a.happened", "", busBase)
	appendBus(t, s, "b.happened", "", busBase.Add(time.Minute))

	n, err := s.TrimBusEvents(busBase.Add(time.Hour))
	if err != nil {
		t.Fatalf("TrimBusEvents: %v", err)
	}
	if n != 2 {
		t.Fatalf("trimmed %d, want 2", n)
	}
}
