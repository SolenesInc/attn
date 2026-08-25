package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Hashing an absent schema to a sentinel rather than the empty string is what
// makes a text<->schema transition flip schemaHash (R-spec R5).
const schemaNoneSentinel = "none"

func hashPrompt(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

func hashSchema(schema json.RawMessage) string {
	if len(schema) == 0 {
		return schemaNoneSentinel
	}
	sum := sha256.Sum256(schema)
	return hex.EncodeToString(sum[:])
}

// The DISPLAY fields below are deliberately NOT part of IsCacheHit and never folded into
// hashPrompt/hashSchema, so renaming a phase never invalidates a cached result.
type JournalEntry struct {
	Ordinal     string
	PromptHash  string
	SchemaHash  string
	Result      json.RawMessage
	Status      string
	Err         string
	Label       string
	Phase       string
	Model       string
	StartedAt   string
	CompletedAt string
}

func isTerminalEntryStatus(status string) bool {
	switch status {
	case "ok", "skipped", "errored":
		return true
	default:
		return false
	}
}

type Journal interface {
	Lookup(ordinal string) (JournalEntry, bool)
	Append(JournalEntry) error
	Upsert(JournalEntry)
	Entries() []JournalEntry
}

// The terminal guard is load-bearing: both journals seed their mirror from ALL persisted
// rows, a crashed "running" row included, which would false-hit and replay a null result.
func IsCacheHit(e JournalEntry, ordinal, promptHash, schemaHash string) bool {
	if !isTerminalEntryStatus(e.Status) {
		return false
	}
	return e.Ordinal == ordinal && e.PromptHash == promptHash && e.SchemaHash == schemaHash
}

type MemJournal struct {
	byOrdinal map[string]int
	order     []JournalEntry
}

func NewMemJournal() *MemJournal {
	return &MemJournal{byOrdinal: map[string]int{}}
}

func (m *MemJournal) Clone() *MemJournal {
	out := NewMemJournal()
	out.order = make([]JournalEntry, len(m.order))
	copy(out.order, m.order)
	for k, v := range m.byOrdinal {
		out.byOrdinal[k] = v
	}
	return out
}

func (m *MemJournal) Lookup(ordinal string) (JournalEntry, bool) {
	idx, ok := m.byOrdinal[ordinal]
	if !ok {
		return JournalEntry{}, false
	}
	return m.order[idx], true
}

func (m *MemJournal) Append(e JournalEntry) error {
	if _, exists := m.byOrdinal[e.Ordinal]; exists {
		return fmt.Errorf("journal: duplicate ordinal %q", e.Ordinal)
	}
	m.byOrdinal[e.Ordinal] = len(m.order)
	m.order = append(m.order, e)
	return nil
}

func (m *MemJournal) Upsert(e JournalEntry) {
	if idx, exists := m.byOrdinal[e.Ordinal]; exists {
		m.order[idx] = e
		return
	}
	m.byOrdinal[e.Ordinal] = len(m.order)
	m.order = append(m.order, e)
}

func (m *MemJournal) Entries() []JournalEntry {
	out := make([]JournalEntry, len(m.order))
	copy(out, m.order)
	return out
}
