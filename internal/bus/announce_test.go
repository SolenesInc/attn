package bus

import (
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
