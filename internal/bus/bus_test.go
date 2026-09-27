package bus

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"
)

type memStore struct {
	mu        sync.Mutex
	events    []Event
	nextSeq   int64
	consumers map[string]Consumer

	sinceErr error
	onDelete func(name string)
}

func newMemStore() *memStore {
	return &memStore{consumers: map[string]Consumer{}}
}

func (m *memStore) Append(e Event, now time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextSeq++
	e.Seq = m.nextSeq
	e.CreatedAt = now
	m.events = append(m.events, e)
	return e.Seq, nil
}

func (m *memStore) Since(cursor int64, limit int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sinceErr != nil {
		return nil, m.sinceErr
	}
	var out []Event
	for _, e := range m.events {
		if e.Seq > cursor {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	if len(out) > 1 {
		sawMultiEventBatch.Reached()
	}
	return out, nil
}

func (m *memStore) Bounds() (int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.events) == 0 {
		return 0, 0, nil
	}
	return m.events[0].Seq, m.events[len(m.events)-1].Seq, nil
}

func (m *memStore) GetConsumer(name string) (Consumer, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.consumers[name]
	return c, ok, nil
}

func (m *memStore) SaveConsumer(c Consumer, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.consumers[c.Name]; ok {
		existing.Filter = c.Filter
		existing.UpdatedAt = now
		m.consumers[c.Name] = existing
		return nil
	}
	c.UpdatedAt = now
	m.consumers[c.Name] = c
	return nil
}

func (m *memStore) DeleteConsumer(name string) error {
	m.mu.Lock()
	hook := m.onDelete
	delete(m.consumers, name)
	m.mu.Unlock()

	if hook != nil {
		hook(name)
	}
	return nil
}

func (m *memStore) SetCursor(name string, cursor int64, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.consumers[name]
	if !ok {
		return errors.New("no such consumer")
	}
	c.Cursor = cursor
	c.UpdatedAt = now
	m.consumers[name] = c
	return nil
}

func (m *memStore) setEnabled(name string, enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.consumers[name]
	c.Enabled = enabled
	m.consumers[name] = c
}

func (m *memStore) ListConsumers() ([]Consumer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Consumer
	for _, c := range m.consumers {
		out = append(out, c)
	}
	return out, nil
}

func (m *memStore) Trim(cutoff time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	floor := int64(-1)
	for _, c := range m.consumers {
		if !c.Enabled && !c.PinsRetention {
			continue
		}
		if floor < 0 || c.Cursor < floor {
			floor = c.Cursor
		}
	}
	if floor < 0 && len(m.events) > 0 {
		floor = m.events[len(m.events)-1].Seq
	}
	kept := m.events[:0]
	removed := 0
	for _, e := range m.events {
		if e.CreatedAt.Before(cutoff) && e.Seq <= floor {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	m.events = kept
	return removed, nil
}

func (m *memStore) Compact(names []string, floor int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	compactable := map[string]bool{}
	for _, n := range names {
		compactable[n] = true
	}
	newest := map[string]int64{}
	for _, e := range m.events {
		if compactable[e.Name] && e.Seq > newest[e.Subject] {
			newest[e.Subject] = e.Seq
		}
	}
	kept := m.events[:0]
	removed := 0
	for _, e := range m.events {
		if compactable[e.Name] && e.Seq <= floor && e.Seq < newest[e.Subject] {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	m.events = kept
	return removed, nil
}

func (m *memStore) Producers(cutoffs []time.Time) ([]ProducerRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	byName := map[string]*ProducerRow{}
	subjects := map[string]map[string]bool{}
	for _, e := range m.events {
		row, ok := byName[e.Name]
		if !ok {
			row = &ProducerRow{Name: e.Name, Recent: make([]int64, len(cutoffs))}
			byName[e.Name] = row
			subjects[e.Name] = map[string]bool{}
		}
		row.Events++
		row.Bytes += int64(len(e.Name) + len(e.Subject) + len(e.Payload) + len(e.Source))
		subjects[e.Name][e.Subject] = true
		for i, c := range cutoffs {
			if !e.CreatedAt.Before(c) {
				row.Recent[i]++
			}
		}
	}
	out := make([]ProducerRow, 0, len(byName))
	for name, row := range byName {
		row.Subjects = int64(len(subjects[name]))
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Events != out[j].Events {
			return out[i].Events > out[j].Events
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (m *memStore) EventTimeAt(seq int64) (time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.events {
		if e.Seq >= seq {
			return e.CreatedAt, true, nil
		}
	}
	return time.Time{}, false, nil
}

func (m *memStore) PendingBytes(above int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var bytes int64
	for _, e := range m.events {
		if e.Seq > above {
			bytes += int64(len(e.Name) + len(e.Subject) + len(e.Payload) + len(e.Source))
		}
	}
	return bytes, nil
}

func (m *memStore) dropEventsBelow(seq int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var kept []Event
	for _, e := range m.events {
		if e.Seq >= seq {
			kept = append(kept, e)
		}
	}
	m.events = kept
}

type recorder struct {
	mu     sync.Mutex
	names  []string
	seqs   []int64
	seen   map[int64]bool
	failOn map[int64]int
}

func newRecorder() *recorder {
	return &recorder{seen: map[int64]bool{}, failOn: map[int64]int{}}
}

func (r *recorder) handle(_ context.Context, ev Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen[ev.Seq] = true
	if n := r.failOn[ev.Seq]; n > 0 {
		r.failOn[ev.Seq] = n - 1
		r.seqs = append(r.seqs, ev.Seq)
		r.names = append(r.names, ev.Name)
		return errors.New("handler boom")
	}
	r.seqs = append(r.seqs, ev.Seq)
	r.names = append(r.names, ev.Name)
	return nil
}

func (r *recorder) snapshot() ([]string, []int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.names...), append([]int64(nil), r.seqs...)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.seqs)
}

func testBus(t *testing.T, s Store) *Bus {
	t.Helper()
	return New(Options{
		Store:        s,
		Log:          func(string, ...interface{}) {},
		PollInterval: 5 * time.Millisecond,
		RetryBase:    5 * time.Millisecond,
		RetryCap:     20 * time.Millisecond,
		TrimInterval: time.Hour,
	})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestConsumerBelowTheTrimPointResumesAtHead(t *testing.T) {
	s := newMemStore()

	seed := testBus(t, s)
	rec0 := newRecorder()
	if err := seed.Register("stale", All, rec0.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := seed.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := seed.Publish("a.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the seed delivery", func() bool { return rec0.count() == 1 })
	seed.Stop()

	offline := testBus(t, s)
	for _, name := range []string{"b.happened", "c.happened", "d.happened"} {
		if _, err := offline.Publish(name, "", nil); err != nil {
			t.Fatalf("Publish(%s): %v", name, err)
		}
	}
	s.dropEventsBelow(4)

	revived := testBus(t, s)
	rec := newRecorder()
	if err := revived.Register("stale", All, rec.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := revived.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(revived.Stop)

	waitFor(t, "the cursor to jump to head", func() bool {
		c, _, _ := s.GetConsumer("stale")
		return c.Cursor == 4
	})
	if rec.count() != 0 {
		names, _ := rec.snapshot()
		t.Fatalf("resumed consumer replayed a partial window: %v", names)
	}

	if _, err := revived.Publish("e.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "delivery after resuming", func() bool { return rec.count() == 1 })
	names, _ := rec.snapshot()
	if names[0] != "e.happened" {
		t.Fatalf("after resuming at head, delivered %v", names)
	}
}

func TestBackoffDoesNotRatchetAcrossSuccessfulDeliveries(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	const backlog = 30
	for i := 0; i < backlog; i++ {
		if _, err := s.Append(Event{Name: "a.happened", Subject: "s"}, time.Now()); err != nil {
			t.Fatalf("seeding the log: %v", err)
		}
	}
	if err := s.SaveConsumer(Consumer{Name: "laggard", Cursor: 0, Enabled: true}, time.Now()); err != nil {
		t.Fatalf("seeding the consumer: %v", err)
	}

	var mu sync.Mutex
	attempts := map[int64][]int{}
	failed := map[int64]bool{}
	handler := func(_ context.Context, ev Event) error {
		mu.Lock()
		streak := b.durables[0].drainFailures()
		attempts[ev.Seq] = append(attempts[ev.Seq], streak)
		first := !failed[ev.Seq]
		if first && (ev.Seq == 1 || ev.Seq == 20) {
			failed[ev.Seq] = true
			mu.Unlock()
			return errors.New("handler boom")
		}
		mu.Unlock()
		return nil
	}

	if err := b.Register("laggard", All, handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	waitFor(t, "the backlog to be consumed", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(attempts) == backlog
	})

	mu.Lock()
	seq20 := append([]int(nil), attempts[20]...)
	mu.Unlock()

	if len(seq20) == 0 {
		t.Fatalf("seq 20 was never delivered")
	}
	if seq20[0] != 0 {
		t.Fatalf("seq 20 was delivered as retry attempt %d; the streak from seq 1 was never reset (all attempts: %v)",
			seq20[0]+1, attempts)
	}
}

func TestKillSwitchStopsASaturatedConsumer(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	var mu sync.Mutex
	delivered := 0
	flipped := false
	handler := func(_ context.Context, _ Event) error {
		mu.Lock()
		delivered++
		n := delivered
		mu.Unlock()
		if n == 10 {
			s.setEnabled("saturated", false)
			mu.Lock()
			flipped = true
			mu.Unlock()
		}
		time.Sleep(time.Millisecond)
		return nil
	}

	if err := b.Register("saturated", All, handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	const total = 600
	for i := 0; i < total; i++ {
		if _, err := b.Publish("a.happened", "s", nil); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}

	waitFor(t, "the kill switch to be flipped", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return flipped
	})

	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	got := delivered
	mu.Unlock()
	if got >= total {
		t.Fatalf("the kill switch never took effect: delivered all %d events", got)
	}
	t.Logf("delivered %d of %d before the kill switch took effect", got, total)
	if got > 100 {
		t.Fatalf("delivery ran on for %d events after the consumer was disabled", got)
	}
}
