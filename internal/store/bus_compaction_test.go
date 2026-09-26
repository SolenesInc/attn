package store

import (
	"slices"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func factsOnLog(t interface {
	Helper()
	Fatalf(string, ...any)
}, s *Store) []BusEvent {
	t.Helper()
	events, err := s.BusEventsSince(0, 100000)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	return events
}

func TestCompactionKeepsTheNewestFactOfEverySubjectAtOrBelowTheFloor(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	names := []string{"document.changed", "document.collection.removed", "session.state.changed", "ticket.created"}
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)

	rapid.Check(t, func(t *rapid.T) {
		if _, err := s.db.Exec(`DELETE FROM bus_events`); err != nil {
			t.Fatal(err)
		}
		compactable := rapid.SliceOfDistinct(rapid.SampledFrom(names), rapid.ID[string]).Draw(t, "compactable")
		count := rapid.IntRange(0, 40).Draw(t, "facts")
		for i := range count {
			fact := BusEvent{
				Name:    rapid.SampledFrom(names).Draw(t, "name"),
				Subject: rapid.SampledFrom([]string{"a", "b", "c"}).Draw(t, "subject"),
				Payload: `{}`,
				Source:  "test",
			}
			if _, err := s.AppendBusEvent(fact, now.Add(time.Duration(i)*time.Second)); err != nil {
				t.Fatal(err)
			}
		}
		before := factsOnLog(t, s)
		floor := int64(0)
		if len(before) > 0 {
			floor = rapid.Int64Range(before[0].Seq-1, before[len(before)-1].Seq).Draw(t, "floor")
		}

		removed, err := s.CompactBusEvents(compactable, floor)
		if err != nil {
			t.Fatal(err)
		}

		newest := map[string]int64{}
		for _, e := range before {
			if slices.Contains(compactable, e.Name) {
				newest[e.Subject] = e.Seq
			}
		}
		var want []int64
		for _, e := range before {
			if slices.Contains(compactable, e.Name) && e.Seq <= floor && e.Seq < newest[e.Subject] {
				continue
			}
			want = append(want, e.Seq)
		}
		var got []int64
		for _, e := range factsOnLog(t, s) {
			got = append(got, e.Seq)
		}
		if !slices.Equal(got, want) || removed != len(before)-len(want) {
			t.Fatalf("compacting %v at floor %d over %v removed %d and left %v, want %v", compactable, floor, before, removed, got, want)
		}
	})
}
