package store

import (
	"sync"

	"github.com/victorarias/attn/internal/docstore"
)

// recentDocumentWrites is how many written ids a table keeps for refreshing a memo instead of reading it again. The
// largest plot (81 children) and the busiest minute of seed writes (94) on a 2,520-seed garden, 2026-11-01, fit
// many times over; a memo further behind reads the collection again.
const recentDocumentWrites = 1024

type documentWrites struct {
	mu      sync.Mutex
	byTable map[string]*tableDocumentWrites
}

type tableDocumentWrites struct {
	writes uint64
	recent []string
}

func (w *documentWrites) table(table string) *tableDocumentWrites {
	if w.byTable == nil {
		w.byTable = map[string]*tableDocumentWrites{}
	}
	t, ok := w.byTable[table]
	if !ok {
		t = &tableDocumentWrites{}
		w.byTable[table] = t
	}
	return t
}

func (w *documentWrites) count(table, id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	t := w.table(table)
	t.writes++
	t.recent = append(t.recent, id)
	if len(t.recent) > recentDocumentWrites {
		t.recent = append(t.recent[:0], t.recent[len(t.recent)-recentDocumentWrites:]...)
	}
}

func (w *documentWrites) reset(table string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	t := w.table(table)
	t.writes++
	t.recent = nil
}

type documentsGeneration struct {
	source *documentWrites
	table  string
	writes uint64
}

func (w *documentWrites) generation(table string) documentsGeneration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return documentsGeneration{source: w, table: table, writes: w.table(table).writes}
}

func (w *documentWrites) writtenSince(gen documentsGeneration) ([]string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if gen.source != w {
		return nil, false
	}
	t := w.table(gen.table)
	behind := t.writes - gen.writes
	if behind > uint64(len(t.recent)) {
		return nil, false
	}
	return append([]string(nil), t.recent[uint64(len(t.recent))-behind:]...), true
}

// DocumentsMemo keeps a value derived from one document collection until the collection's next write. Every document
// write holds s.mu exclusively until it commits, and RememberDocuments returns only a value whose generation is current
// under s.mu, so a returned value is never older than the last committed write.
type DocumentsMemo[T any] struct {
	reading sync.Mutex
	mu      sync.Mutex
	gen     documentsGeneration
	kept    bool
	value   T
}

// RememberDocuments returns the kept value while the collection is unchanged. After writes it hands the kept value and
// the written ids to refresh, or calls read when it kept nothing or the writes outran what the table remembers. Both run
// outside s.mu, one caller at a time, because a cold read of a large collection takes longer than any writer can wait.
func RememberDocuments[T any](
	s *Store, memo *DocumentsMemo[T], schema docstore.CollectionSchema,
	read func() (T, error), refresh func(previous T, written []string) (T, error),
) (T, error) {
	memo.reading.Lock()
	defer memo.reading.Unlock()
	for {
		s.mu.RLock()
		gen := s.writes.documents.generation(schema.Table)
		previous, previousGen, kept := memo.lookup()
		written, known := s.writes.documents.writtenSince(previousGen)
		s.mu.RUnlock()
		if kept && previousGen == gen {
			return previous, nil
		}
		var value T
		var err error
		if kept && known && previousGen.table == gen.table {
			value, err = refresh(previous, written)
		} else {
			value, err = read()
		}
		if err != nil {
			return value, err
		}
		memo.keep(gen, value)
	}
}

func (m *DocumentsMemo[T]) lookup() (T, documentsGeneration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.value, m.gen, m.kept
}

func (m *DocumentsMemo[T]) keep(gen documentsGeneration, value T) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gen, m.kept, m.value = gen, true, value
}
