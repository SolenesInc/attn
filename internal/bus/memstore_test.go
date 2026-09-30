package bus

import (
	"errors"
	"sort"
	"sync"
	"time"
)

type memStore struct {
	mu        sync.Mutex
	events    []Event
	nextSeq   int64
	consumers map[string]Consumer
	// attempts, when set, receives every SetCursor outcome so a test can wait
	// on the drain instead of sleeping.
	attempts chan cursorAttempt
}

type cursorAttempt struct {
	cursor  int64
	applied bool
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
	var out []Event
	for _, e := range m.events {
		if e.Seq > cursor {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
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
	defer m.mu.Unlock()
	delete(m.consumers, name)
	return nil
}

func (m *memStore) SetCursor(name string, cursor int64, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.consumers[name]
	if !ok {
		return false, errors.New("no such consumer")
	}
	if c.Enabled {
		c.Cursor = cursor
		c.UpdatedAt = now
		m.consumers[name] = c
	}
	if m.attempts != nil {
		m.attempts <- cursorAttempt{cursor: cursor, applied: c.Enabled}
	}
	return c.Enabled, nil
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
