package store

import (
	"fmt"
	"testing"
	"time"
)

func changeOf(subject string) BusEvent {
	return BusEvent{Name: "document.changed", Subject: subject, Payload: `{}`, Source: "test"}
}

func seqsOnLog(t *testing.T, s *Store) []int64 {
	t.Helper()
	events, err := s.BusEventsSince(0, 100000)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	seqs := make([]int64, 0, len(events))
	for _, e := range events {
		seqs = append(seqs, e.Seq)
	}
	return seqs
}

func headOf(t *testing.T, s *Store) int64 {
	t.Helper()
	_, head, err := s.BusBounds()
	if err != nil {
		t.Fatalf("bounds: %v", err)
	}
	return head
}

func TestChurningOneSubjectLeavesOneFact(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)

	var last int64
	for i := range 200 {
		seq, err := s.AppendBusEvent(changeOf("app/x/requests/a"), now.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		last = seq
	}

	removed, err := s.CompactBusEvents([]string{"document.changed"}, headOf(t, s))
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if removed != 199 {
		t.Fatalf("compaction removed %d fact(s), want 199", removed)
	}
	if got := seqsOnLog(t, s); len(got) != 1 || got[0] != last {
		t.Fatalf("log holds %v, want only the newest fact at %d", got, last)
	}
}

func TestCompactionKeepsTheNewestOfEverySubjectItTouches(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)

	newest := map[string]int64{}
	for i := range 30 {
		for _, subject := range []string{"app/x/requests/a", "app/x/requests/b"} {
			seq, err := s.AppendBusEvent(changeOf(subject), now.Add(time.Duration(i)*time.Second))
			if err != nil {
				t.Fatalf("append: %v", err)
			}
			newest[subject] = seq
		}
		if _, err := s.AppendBusEvent(BusEvent{
			Name: "session.state.changed", Subject: "app/x/requests/a", Source: "test",
		}, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	if _, err := s.CompactBusEvents([]string{"document.changed"}, headOf(t, s)); err != nil {
		t.Fatalf("compact: %v", err)
	}

	events, err := s.BusEventsSince(0, 100000)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	changes, others := map[string]int64{}, 0
	for _, e := range events {
		if e.Name == "document.changed" {
			if _, dup := changes[e.Subject]; dup {
				t.Fatalf("subject %s kept more than one fact", e.Subject)
			}
			changes[e.Subject] = e.Seq
			continue
		}
		others++
	}
	if len(changes) != 2 {
		t.Fatalf("compaction left facts for %d subject(s), want 2", len(changes))
	}
	for subject, seq := range changes {
		if seq != newest[subject] {
			t.Fatalf("subject %s kept seq %d, want the newest at %d", subject, seq, newest[subject])
		}
	}
	if others != 30 {
		t.Fatalf("compaction touched an unnamed fact class: %d survived, want 30", others)
	}
}

func TestAConsumerParkedBelowTheFloorPinsEveryFactItHasNotRead(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)

	var seqs []int64
	for i := range 10 {
		seq, err := s.AppendBusEvent(changeOf("app/x/requests/a"), now.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		seqs = append(seqs, seq)
	}

	floor := seqs[2]
	if err := s.SaveBusConsumer(BusConsumer{Name: "slowpoke", Cursor: floor, Enabled: true}, now); err != nil {
		t.Fatalf("registering the consumer: %v", err)
	}

	if _, err := s.CompactBusEvents([]string{"document.changed"}, floor); err != nil {
		t.Fatalf("compact: %v", err)
	}

	got := seqsOnLog(t, s)
	want := seqs[3:]
	if len(got) != len(want) {
		t.Fatalf("log holds %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("log holds %v, want %v", got, want)
		}
	}
}

func TestCompactingNoNamesRemovesNothing(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	for range 5 {
		if _, err := s.AppendBusEvent(changeOf("app/x/requests/a"), now); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	removed, err := s.CompactBusEvents(nil, headOf(t, s))
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if removed != 0 || len(seqsOnLog(t, s)) != 5 {
		t.Fatalf("compacting no names removed %d fact(s), leaving %d", removed, len(seqsOnLog(t, s)))
	}
}

func TestACompactedLogIsNoLargerThanWhatItDescribes(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)

	const documents = 20
	live := map[string]bool{}
	tombstones := map[string]bool{}
	step := 0
	appendChange := func(id string, deleted bool) {
		step++
		if _, err := s.AppendBusEvent(changeOf("app/x/requests/"+id), now.Add(time.Duration(step)*time.Second)); err != nil {
			t.Fatalf("append: %v", err)
		}
		if deleted {
			delete(live, id)
			tombstones[id] = true
			return
		}
		live[id] = true
		delete(tombstones, id)
	}

	for i := range documents {
		appendChange(fmt.Sprintf("d%02d", i), false)
	}
	for range 8 {
		for i := range documents {
			appendChange(fmt.Sprintf("d%02d", i), false)
		}
	}
	for i := 0; i < documents; i += 3 {
		appendChange(fmt.Sprintf("d%02d", i), true)
	}

	before := len(seqsOnLog(t, s))
	if _, err := s.CompactBusEvents([]string{"document.changed"}, headOf(t, s)); err != nil {
		t.Fatalf("compact: %v", err)
	}
	after := len(seqsOnLog(t, s))

	if bound := len(live) + len(tombstones); after > bound {
		t.Fatalf("compacted log holds %d fact(s) for %d live document(s) and %d tombstone(s)",
			after, len(live), len(tombstones))
	}
	if after >= before {
		t.Fatalf("compaction of %d fact(s) about %d document(s) removed nothing", before, documents)
	}
}

func TestTheLogReportsItsOwnWeight(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)

	rows, bytes, err := s.BusLogSize()
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if rows != 0 || bytes != 0 {
		t.Fatalf("an empty log weighs %d row(s), %d byte(s)", rows, bytes)
	}

	for range 4 {
		if _, err := s.AppendBusEvent(changeOf("app/x/requests/a"), now); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	rows, bytes, err = s.BusLogSize()
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if rows != 4 {
		t.Fatalf("log holds %d row(s), want 4", rows)
	}
	one := changeOf("app/x/requests/a")
	if want := int64(4 * (len(one.Name) + len(one.Subject) + len(one.Payload) + len(one.Source))); bytes <= want {
		t.Fatalf("4 facts weigh %d byte(s), want more than the %d of their own text", bytes, want)
	}

	if _, err := s.CompactBusEvents([]string{"document.changed"}, headOf(t, s)); err != nil {
		t.Fatalf("compact: %v", err)
	}
	compacted, compactedBytes, err := s.BusLogSize()
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if compacted != 1 || compactedBytes >= bytes {
		t.Fatalf("after compaction the log reports %d row(s), %d byte(s); was %d/%d",
			compacted, compactedBytes, rows, bytes)
	}
}
