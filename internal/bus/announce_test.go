package bus

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func watchAll(b *Bus) (func() []Event, func()) {
	var (
		mu   sync.Mutex
		seen []Event
	)
	stop := b.Subscribe(Filter{}, func(ev Event) {
		mu.Lock()
		seen = append(seen, ev)
		mu.Unlock()
	})
	snapshot := func() []Event {
		mu.Lock()
		defer mu.Unlock()
		return append([]Event(nil), seen...)
	}
	return snapshot, stop
}

func seqsOf(events []Event) []int64 {
	out := make([]int64, 0, len(events))
	for _, e := range events {
		out = append(out, e.Seq)
	}
	return out
}

func inOrder(seqs []int64) bool {
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			return false
		}
	}
	return true
}

func TestAnnounceDeliversWhatTheLogGainedWithoutPublish(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	seen, stop := watchAll(b)
	defer stop()

	now := time.Now()
	first := s.appendOutOfBand("document.changed", "app/x/requests/a", now)
	second := s.appendOutOfBand("document.changed", "app/x/requests/b", now)

	b.Announce()

	got := seqsOf(seen())
	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("announce delivered %v, want [%d %d]", got, first, second)
	}
}

func TestAnnouncingTwiceDeliversOnce(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	seen, stop := watchAll(b)
	defer stop()

	s.appendOutOfBand("document.changed", "app/x/requests/a", time.Now())
	b.Announce()
	b.Announce()
	b.Announce()

	if got := seen(); len(got) != 1 {
		t.Fatalf("three announces of one fact delivered %d time(s)", len(got))
	}
}

func TestAnnounceDoesNotReplayWhatWasThereBeforeTheBus(t *testing.T) {
	s := newMemStore()
	now := time.Now()
	s.appendOutOfBand("document.changed", "app/x/requests/old", now)
	s.appendOutOfBand("document.changed", "app/x/requests/older", now)

	b := testBus(t, s)
	seen, stop := watchAll(b)
	defer stop()

	b.Announce()
	if got := seen(); len(got) != 0 {
		t.Fatalf("a fresh bus replayed %d historical fact(s)", len(got))
	}

	fresh := s.appendOutOfBand("document.changed", "app/x/requests/new", now)
	b.Announce()
	got := seqsOf(seen())
	if len(got) != 1 || got[0] != fresh {
		t.Fatalf("announce delivered %v, want only the new fact at %d", got, fresh)
	}
}

func TestRacingWritersDeliverTheLogsOrderExactlyOnce(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	seen, stop := watchAll(b)
	defer stop()

	const writers = 8
	const each = 25

	var (
		start sync.WaitGroup
		done  sync.WaitGroup
	)
	start.Add(1)
	for w := range writers {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			for i := range each {
				if w%2 == 0 {
					s.appendOutOfBand("document.changed", "app/x/requests/a", time.Now())
					b.Announce()
					continue
				}
				if _, err := b.Publish("session.state.changed", "s", map[string]int{"i": i}); err != nil {
					t.Errorf("publish: %v", err)
					return
				}
			}
		}()
	}
	start.Done()
	done.Wait()
	b.Announce()

	got := seqsOf(seen())
	if want := writers * each; len(got) != want {
		t.Fatalf("delivered %d fact(s), want %d — a fact was dropped or doubled", len(got), want)
	}
	if !inOrder(got) {
		t.Fatalf("delivered out of the log's order: %v", got)
	}
}

func TestAnnounceWithoutAStoreDoesNothing(t *testing.T) {
	b := New(Options{Log: func(string, ...interface{}) {}})
	seen, stop := watchAll(b)
	defer stop()

	b.Announce()
	if got := seen(); len(got) != 0 {
		t.Fatalf("a store-less bus announced %d fact(s)", len(got))
	}
}

func TestTrimCompactsTheNamesTheBusDeclared(t *testing.T) {
	s := newMemStore()
	b := New(Options{
		Store:       s,
		Log:         func(string, ...interface{}) {},
		Compactable: []string{"document.changed"},
		Retention:   time.Hour,
	})

	now := time.Now()
	for range 10 {
		s.appendOutOfBand("document.changed", "app/x/requests/a", now)
	}
	for range 3 {
		s.appendOutOfBand("session.state.changed", "sess-1", now)
	}

	removed, err := b.Trim()
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	if removed != 9 {
		t.Fatalf("trim removed %d fact(s), want the 9 redundant ones", removed)
	}

	status, err := b.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Rows != 4 {
		t.Fatalf("log holds %d row(s) after compaction, want 4", status.Rows)
	}
	if status.Bytes <= 0 {
		t.Fatalf("a non-empty log reports %d byte(s)", status.Bytes)
	}
}

// Compaction is opt-in per fact class: it is only sound for facts that are pure
// invalidations.
func TestTrimCompactsNothingWhenNoNameWasDeclared(t *testing.T) {
	s := newMemStore()
	b := New(Options{Store: s, Log: func(string, ...interface{}) {}, Retention: time.Hour})

	now := time.Now()
	for range 10 {
		s.appendOutOfBand("document.changed", "app/x/requests/a", now)
	}

	if removed, err := b.Trim(); err != nil || removed != 0 {
		t.Fatalf("a bus with no compactable names removed %d fact(s)", removed)
	}
	status, err := b.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Rows != 10 {
		t.Fatalf("log holds %d row(s), want all 10", status.Rows)
	}
}

// The mark's zero value means "everything after seq 0", so announcing on an
// unplaced mark replays the entire log into every live client.
func TestABusThatCouldNotFindTheHeadDoesNotReplayTheLog(t *testing.T) {
	s := newMemStore()
	now := time.Now()
	for range 5 {
		s.appendOutOfBand("document.changed", "app/x/requests/old", now)
	}
	s.setBoundsErr(errors.New("the log would not say where it stands"))

	b := testBus(t, s)
	seen, stop := watchAll(b)
	defer stop()

	b.Announce()
	if got := seen(); len(got) != 0 {
		t.Fatalf("a bus with no mark announced %d historical fact(s)", len(got))
	}

	if _, err := b.Publish("session.state.changed", "s-1", nil); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got := seen(); len(got) != 1 {
		t.Fatalf("a publish under an unplaced mark delivered %d event(s), want 1", len(got))
	}

	s.setBoundsErr(nil)
	b.Announce()
	if got := seen(); len(got) != 1 {
		t.Fatalf("placing the mark replayed history: %d event(s) delivered", len(got))
	}
	fresh := s.appendOutOfBand("document.changed", "app/x/requests/new", now)
	b.Announce()
	got := seen()
	if len(got) != 2 || got[1].Seq != fresh {
		t.Fatalf("after the mark was placed, delivered %v", seqsOf(got))
	}
}

func TestTrimReportsAPassThatCouldNotRun(t *testing.T) {
	s := newMemStore()
	b := New(Options{
		Store:       s,
		Log:         func(string, ...interface{}) {},
		Compactable: []string{"document.changed"},
		Retention:   time.Hour,
	})
	now := time.Now()
	for range 4 {
		s.appendOutOfBand("document.changed", "app/x/requests/a", now)
	}

	s.setBoundsErr(errors.New("the log would not say where it stands"))
	removed, err := b.Trim()
	if err == nil {
		t.Fatal("a pass that could not run reported success")
	}
	if removed != 0 {
		t.Fatalf("a failed pass reported removing %d event(s)", removed)
	}

	s.setBoundsErr(nil)
	removed, err = b.Trim()
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	if removed != 3 {
		t.Fatalf("the recovered pass removed %d event(s), want 3", removed)
	}
}
