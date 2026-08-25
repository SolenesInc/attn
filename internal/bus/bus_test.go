package bus

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// memStore's trim semantics deliberately mirror SQLite's (age window AND cursor
// floor), so a passing test is not passing because the fake is more permissive.
type memStore struct {
	mu        sync.Mutex
	events    []Event
	nextSeq   int64
	consumers map[string]Consumer

	sinceErr  error
	appendErr error
	boundsErr error
	deleteErr error
	onDelete  func(name string)
}

func newMemStore() *memStore {
	return &memStore{consumers: map[string]Consumer{}}
}

func (m *memStore) Append(e Event, now time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.appendErr != nil {
		return 0, m.appendErr
	}
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
	if m.boundsErr != nil {
		return 0, 0, m.boundsErr
	}
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
	hook, err := m.onDelete, m.deleteErr
	if err == nil {
		delete(m.consumers, name)
	}
	m.mu.Unlock()

	if hook != nil {
		hook(name)
	}
	return err
}

// SetCursor refuses a name it does not know, as an UPDATE matching no row would.
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

func (m *memStore) setBoundsErr(err error) {
	m.mu.Lock()
	m.boundsErr = err
	m.mu.Unlock()
}

func (m *memStore) setDeleteErr(err error) {
	m.mu.Lock()
	m.deleteErr = err
	m.mu.Unlock()
}

func (m *memStore) setAppendErr(err error) {
	m.mu.Lock()
	m.appendErr = err
	m.mu.Unlock()
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

// Mirrors the SQLite implementation: for the named fact classes keep only the
// newest row per subject, and only touch rows at or below the floor.
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

// Mirrors the SQLite aggregate: one row per fact class, loudest first.
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

func (m *memStore) appendOutOfBand(name, subject string, now time.Time) int64 {
	seq, err := m.Append(Event{Name: name, Subject: subject}, now)
	if err != nil {
		panic(err)
	}
	return seq
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
	if r.seen[ev.Seq] {
		sawRedelivery.Reached()
	}
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

func TestNewConsumerStartsAtHead(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	if _, err := b.Publish("old.fact", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	rec := newRecorder()
	if err := b.Register("late", All, rec.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	if _, err := b.Publish("new.fact", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the post-registration fact", func() bool { return rec.count() >= 1 })

	names, _ := rec.snapshot()
	if len(names) != 1 || names[0] != "new.fact" {
		t.Fatalf("delivered %v; a fresh consumer must not replay history", names)
	}
}

func TestDownedConsumerCatchesUpInOrder(t *testing.T) {
	s := newMemStore()

	first := testBus(t, s)
	rec1 := newRecorder()
	if err := first.Register("worker", All, rec1.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := first.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := first.Publish("a.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the first delivery", func() bool { return rec1.count() == 1 })
	first.Stop()

	offline := testBus(t, s)
	for _, name := range []string{"b.happened", "c.happened", "d.happened"} {
		if _, err := offline.Publish(name, "", nil); err != nil {
			t.Fatalf("Publish(%s): %v", name, err)
		}
	}

	second := testBus(t, s)
	rec2 := newRecorder()
	if err := second.Register("worker", All, rec2.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := second.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(second.Stop)

	waitFor(t, "catch-up", func() bool { return rec2.count() >= 3 })
	names, seqs := rec2.snapshot()
	want := []string{"b.happened", "c.happened", "d.happened"}
	if len(names) != 3 {
		t.Fatalf("delivered %v, want exactly %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("delivered %v, want %v", names, want)
		}
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("out of seq order: %v", seqs)
		}
	}
}

func TestFailingHandlerRedeliversAndDoesNotAdvance(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	rec := newRecorder()
	rec.failOn[1] = 2

	if err := b.Register("flaky", All, rec.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	if _, err := b.Publish("a.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, err := b.Publish("b.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	waitFor(t, "both events to land", func() bool {
		_, seqs := rec.snapshot()
		for _, s := range seqs {
			if s == 2 {
				return true
			}
		}
		return false
	})

	_, seqs := rec.snapshot()
	if len(seqs) != 4 {
		t.Fatalf("delivery sequence %v, want three attempts at seq 1 then seq 2", seqs)
	}
	for i := 0; i < 3; i++ {
		if seqs[i] != 1 {
			t.Fatalf("delivery sequence %v: expected seq 1 redelivered until success", seqs)
		}
	}
	if seqs[3] != 2 {
		t.Fatalf("delivery sequence %v: seq 2 must come after seq 1 succeeds", seqs)
	}

	c, _, _ := s.GetConsumer("flaky")
	if c.Cursor != 2 {
		t.Fatalf("cursor is %d after both events, want 2", c.Cursor)
	}
}

func TestFilteredConsumerSkipsAndStillAdvances(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	rec := newRecorder()
	if err := b.Register("tickets", Filter{"ticket.*"}, rec.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	for _, name := range []string{"session.state.changed", "ticket.commented", "session.registered", "pr.updated"} {
		if _, err := b.Publish(name, "", nil); err != nil {
			t.Fatalf("Publish(%s): %v", name, err)
		}
	}

	waitFor(t, "the cursor to reach head", func() bool {
		c, _, _ := s.GetConsumer("tickets")
		return c.Cursor == 4
	})

	names, _ := rec.snapshot()
	if len(names) != 1 || names[0] != "ticket.commented" {
		t.Fatalf("filtered consumer saw %v, want only ticket.commented", names)
	}
}

func TestEphemeralSubscriberGetsLiveEventsOnly(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	if _, err := b.Publish("before.subscribe", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var (
		mu   sync.Mutex
		seen []string
	)
	cancel := b.Subscribe(Filter{"live.*"}, func(ev Event) {
		mu.Lock()
		seen = append(seen, ev.Name)
		mu.Unlock()
	})

	for _, name := range []string{"live.one", "other.thing", "live.two"} {
		if _, err := b.Publish(name, "", nil); err != nil {
			t.Fatalf("Publish(%s): %v", name, err)
		}
	}
	cancel()
	if _, err := b.Publish("live.three", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != "live.one" || seen[1] != "live.two" {
		t.Fatalf("ephemeral subscriber saw %v, want [live.one live.two]", seen)
	}

	if len(s.consumers) != 0 {
		t.Fatalf("ephemeral subscriber persisted a cursor: %+v", s.consumers)
	}
}

func TestDisabledConsumerStopsAndResumes(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	rec := newRecorder()
	if err := b.Register("killable", All, rec.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	if _, err := b.Publish("a.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, "the first delivery", func() bool { return rec.count() == 1 })

	s.setEnabled("killable", false)
	for _, name := range []string{"b.happened", "c.happened"} {
		if _, err := b.Publish(name, "", nil); err != nil {
			t.Fatalf("Publish(%s): %v", name, err)
		}
	}
	time.Sleep(60 * time.Millisecond)
	if rec.count() != 1 {
		names, _ := rec.snapshot()
		t.Fatalf("a disabled consumer was delivered to: %v", names)
	}

	s.setEnabled("killable", true)
	waitFor(t, "resumed delivery", func() bool { return rec.count() == 3 })
	names, _ := rec.snapshot()
	if names[1] != "b.happened" || names[2] != "c.happened" {
		t.Fatalf("resumed from the wrong place: %v", names)
	}
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

func TestPreDrainOwnsGapRepairAndKeepsLaterFactsBehindItsFence(t *testing.T) {
	s := newMemStore()
	for _, name := range []string{"one", "two", "three", "four"} {
		if _, err := s.Append(Event{Name: name}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	s.dropEventsBelow(4)
	s.consumers["app:projection"] = Consumer{Name: "app:projection", Cursor: 1, Filter: "*", Enabled: true}

	b := testBus(t, s)
	rec := newRecorder()
	var got *Gap
	checks := 0
	d := b.newDurable("app:projection", All, func(_ context.Context, consumer Consumer, gap *Gap) error {
		checks++
		if checks == 2 {
			if consumer.Cursor != 5 || gap != nil {
				t.Fatalf("second pre-drain consumer/gap = %+v / %+v", consumer, gap)
			}
			return nil
		}
		if consumer.Cursor != 1 || gap == nil {
			t.Fatalf("pre-drain consumer/gap = %+v / %+v", consumer, gap)
		}
		copy := *gap
		got = &copy
		if err := s.SetCursor(consumer.Name, gap.Head, time.Now()); err != nil {
			return err
		}
		_, err := s.Append(Event{Name: "after-fence"}, time.Now())
		return err
	}, rec.handle)
	t.Cleanup(d.cancel)
	if err := b.drain(d); err != nil {
		t.Fatal(err)
	}
	if checks != 2 || got == nil || got.Cursor != 1 || got.Earliest != 4 || got.Head != 4 || got.Missed != 2 {
		t.Fatalf("checks/gap = %d / %+v", checks, got)
	}
	names, seqs := rec.snapshot()
	if len(names) != 1 || names[0] != "after-fence" || seqs[0] != 5 {
		t.Fatalf("delivered names/seqs = %v / %v", names, seqs)
	}
	c, _, _ := s.GetConsumer("app:projection")
	if c.Cursor != 5 {
		t.Fatalf("cursor = %d, want later fact 5", c.Cursor)
	}
}

func TestPreDrainFailureMovesNeitherGapNorFactCursor(t *testing.T) {
	s := newMemStore()
	for _, name := range []string{"one", "two", "three"} {
		if _, err := s.Append(Event{Name: name}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	s.dropEventsBelow(3)
	s.consumers["app:projection"] = Consumer{Name: "app:projection", Cursor: 0, Filter: "*", Enabled: true}
	b := testBus(t, s)
	rec := newRecorder()
	d := b.newDurable("app:projection", All, func(context.Context, Consumer, *Gap) error {
		return errors.New("reconcile still owed")
	}, rec.handle)
	t.Cleanup(d.cancel)
	if err := b.drain(d); err == nil || !strings.Contains(err.Error(), "reconcile still owed") {
		t.Fatalf("drain error = %v", err)
	}
	c, _, _ := s.GetConsumer("app:projection")
	if c.Cursor != 0 || rec.count() != 0 {
		t.Fatalf("failed pre-drain moved cursor/delivered: cursor=%d delivered=%d", c.Cursor, rec.count())
	}
}

func TestPreDrainRechecksObligationsBetweenBatches(t *testing.T) {
	s := newMemStore()
	if _, err := s.Append(Event{Name: "before-trigger"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.consumers["app:projection"] = Consumer{Name: "app:projection", Filter: "*", Enabled: true}
	b := testBus(t, s)
	b.batchSize = 1

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	owed := false
	checks := 0
	d := b.newDurable("app:projection", All, func(context.Context, Consumer, *Gap) error {
		checks++
		if owed {
			return errors.New("reconcile still owed")
		}
		return nil
	}, func(ctx context.Context, ev Event) error {
		close(firstStarted)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-releaseFirst:
			return nil
		}
	})
	t.Cleanup(d.cancel)

	drained := make(chan error, 1)
	go func() { drained <- b.drain(d) }()
	<-firstStarted
	owed = true
	for _, name := range []string{"after-trigger-one", "after-trigger-two"} {
		if _, err := s.Append(Event{Name: name}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	close(releaseFirst)

	if err := <-drained; err == nil || !strings.Contains(err.Error(), "reconcile still owed") {
		t.Fatalf("drain error = %v", err)
	}
	consumer, _, _ := s.GetConsumer("app:projection")
	if checks != 2 || consumer.Cursor != 1 {
		t.Fatalf("pre-drain checks/cursor = %d/%d, want 2/1", checks, consumer.Cursor)
	}
}

func TestStatusReportsLagAndLiveness(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	rec := newRecorder()
	rec.failOn[1] = 100

	if err := b.Register("stuck", All, rec.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	for i := 0; i < 3; i++ {
		if _, err := b.Publish("a.happened", "", nil); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	waitFor(t, "a stall to be recorded", func() bool {
		st, err := b.Status()
		if err != nil || len(st.Consumers) != 1 {
			return false
		}
		return st.Consumers[0].Stalled != ""
	})

	st, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Head != 3 {
		t.Fatalf("head is %d, want 3", st.Head)
	}
	c := st.Consumers[0]
	if c.Lag != 3 {
		t.Fatalf("lag is %d, want 3 (cursor stuck at 0)", c.Lag)
	}
	if !c.Live {
		t.Fatal("a consumer with a running loop should report Live")
	}
}

func TestRegisterRejectsDuplicatesAndBlanks(t *testing.T) {
	b := testBus(t, newMemStore())
	h := func(context.Context, Event) error { return nil }

	if err := b.Register("dup", All, h); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Register("dup", All, h); err == nil {
		t.Fatal("duplicate consumer name accepted")
	}
	if err := b.Register("  ", All, h); err == nil {
		t.Fatal("blank consumer name accepted")
	}
	if err := b.Register("nohandler", All, nil); err == nil {
		t.Fatal("nil handler accepted")
	}
}

func TestPublishRequiresAName(t *testing.T) {
	b := testBus(t, newMemStore())
	if _, err := b.Publish("  ", "", nil); err == nil {
		t.Fatal("publish with a blank name accepted")
	}
}

func TestPayloadRoundTrip(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	type body struct {
		State string `json:"state"`
	}
	got := make(chan body, 1)
	if err := b.Register("reader", All, func(_ context.Context, ev Event) error {
		var v body
		if err := ev.Decode(&v); err != nil {
			return err
		}
		got <- v
		return nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	if _, err := b.Publish("session.state.changed", "sess_1", body{State: "idle"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case v := <-got:
		if v.State != "idle" {
			t.Fatalf("payload round-trip gave %+v", v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the payload")
	}
}

func TestNilStoreIsInert(t *testing.T) {
	b := New(Options{})
	if err := b.Register("x", All, func(context.Context, Event) error { return nil }); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start with no store: %v", err)
	}
	t.Cleanup(b.Stop)

	seq, err := b.Publish("a.happened", "", nil)
	if err != nil || seq != 0 {
		t.Fatalf("Publish on a store-less bus = (%d, %v)", seq, err)
	}
	if st, err := b.Status(); err != nil || st.Head != 0 {
		t.Fatalf("Status on a store-less bus = (%+v, %v)", st, err)
	}
}

func TestTrimIsCursorAware(t *testing.T) {
	s := newMemStore()
	clk := &testClock{now: time.Now()}
	b := New(Options{
		Store:        s,
		Now:          clk.get,
		Retention:    time.Hour,
		TrimInterval: time.Hour,
		PollInterval: 5 * time.Millisecond,
	})

	rec := newRecorder()
	if err := b.Register("slow", All, rec.handle); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(b.Stop)

	waitFor(t, "the consumer to be registered", func() bool {
		_, ok, _ := s.GetConsumer("slow")
		return ok
	})

	clk.advance(-4 * time.Hour)
	if _, err := b.Publish("a.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, err := b.Publish("b.happened", "", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	clk.advance(4 * time.Hour)

	waitFor(t, "both events to be consumed", func() bool { return rec.count() == 2 })
	if n, err := b.Trim(); err != nil || n != 2 {
		t.Fatalf("trimmed %d events after they were consumed, want 2", n)
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

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) get() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func TestFailedAppendStillFansOutToSubscribers(t *testing.T) {
	s := newMemStore()
	b := testBus(t, s)

	var mu sync.Mutex
	var seen []Event
	cancel := b.Subscribe(All, func(ev Event) {
		mu.Lock()
		seen = append(seen, ev)
		mu.Unlock()
	})
	t.Cleanup(cancel)

	s.setAppendErr(errors.New("disk had a bad night"))

	seq, err := b.Publish("ticket.created", "t-1", nil)
	if err == nil {
		t.Fatalf("Publish reported success despite a failed append")
	}
	if seq != 0 {
		t.Fatalf("a non-durable fact got seq %d, want 0", seq)
	}

	mu.Lock()
	got := append([]Event(nil), seen...)
	mu.Unlock()

	if len(got) != 1 {
		t.Fatalf("the subscriber saw %d event(s); a failed append silenced the wire", len(got))
	}
	if got[0].Name != "ticket.created" || got[0].Subject != "t-1" {
		t.Fatalf("fanned out the wrong event: %+v", got[0])
	}
	if got[0].Seq != 0 {
		t.Fatalf("a non-durable event carried seq %d, want 0 as the marker", got[0].Seq)
	}

	s.setAppendErr(nil)
	seq, err = b.Publish("ticket.created", "t-2", nil)
	if err != nil {
		t.Fatalf("Publish after recovery: %v", err)
	}
	if seq == 0 {
		t.Fatalf("the fact after recovery was not made durable")
	}
}
