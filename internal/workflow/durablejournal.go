package workflow

import (
	"encoding/json"
	"fmt"

	"github.com/victorarias/attn/internal/store"
)

type WorkflowJournalStore interface {
	UpsertWorkflowAgentCall(call *store.WorkflowAgentCallRow) error
	ListWorkflowAgentCalls(runID string) ([]*store.WorkflowAgentCallRow, error)
}

type DurableJournal struct {
	store   WorkflowJournalStore
	runID   string
	mirror  *MemJournal
	lastErr error
}

func NewDurableJournal(s WorkflowJournalStore, runID string) *DurableJournal {
	dj := &DurableJournal{
		store:  s,
		runID:  runID,
		mirror: NewMemJournal(),
	}
	rows, err := s.ListWorkflowAgentCalls(runID)
	if err != nil {
		dj.lastErr = err
		return dj
	}
	for _, row := range rows {
		dj.mirror.Upsert(entryFromRow(row))
	}
	return dj
}

func (d *DurableJournal) Lookup(ordinal string) (JournalEntry, bool) {
	return d.mirror.Lookup(ordinal)
}

func (d *DurableJournal) Append(e JournalEntry) error {
	if _, exists := d.mirror.Lookup(e.Ordinal); exists {
		return fmt.Errorf("journal: duplicate ordinal %q", e.Ordinal)
	}
	if err := d.store.UpsertWorkflowAgentCall(rowFromEntry(d.runID, e)); err != nil {
		return err
	}
	_ = d.mirror.Append(e)
	return nil
}

func (d *DurableJournal) Upsert(e JournalEntry) {
	d.mirror.Upsert(e)
	if err := d.store.UpsertWorkflowAgentCall(rowFromEntry(d.runID, e)); err != nil {
		d.lastErr = err
	}
}

func (d *DurableJournal) Entries() []JournalEntry {
	return d.mirror.Entries()
}

func (d *DurableJournal) Err() error {
	return d.lastErr
}

// In-daemon twin of callFromEntry (cmd/attn/workflow_ipcjournal.go); the two must
// stay behaviorally consistent.
func rowFromEntry(runID string, e JournalEntry) *store.WorkflowAgentCallRow {
	return &store.WorkflowAgentCallRow{
		RunID:         runID,
		Ordinal:       e.Ordinal,
		Label:         ptrIfNonEmpty(e.Label),
		Phase:         ptrIfNonEmpty(e.Phase),
		ResolvedModel: ptrIfNonEmpty(e.Model),
		PromptHash:    ptrIfNonEmpty(e.PromptHash),
		SchemaHash:    ptrIfNonEmpty(e.SchemaHash),
		ResultJSON:    rawMessageToPtr(e.Result),
		Status:        e.Status,
		Error:         ptrIfNonEmpty(e.Err),
		StartedAt:     ptrIfNonEmpty(e.StartedAt),
		CompletedAt:   ptrIfNonEmpty(e.CompletedAt),
	}
}

// entryFromRow must stay lossless for all six JournalEntry fields: IsCacheHit
// depends on Ordinal+PromptHash+SchemaHash, and replay uses Result/Status.
func entryFromRow(row *store.WorkflowAgentCallRow) JournalEntry {
	return JournalEntry{
		Ordinal:    row.Ordinal,
		PromptHash: derefOr(row.PromptHash),
		SchemaHash: derefOr(row.SchemaHash),
		Result:     ptrToRawMessage(row.ResultJSON),
		Status:     row.Status,
		Err:        derefOr(row.Error),
	}
}

func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func rawMessageToPtr(r json.RawMessage) *string {
	if len(r) == 0 {
		return nil
	}
	s := string(r)
	return &s
}

func ptrToRawMessage(s *string) json.RawMessage {
	if s == nil {
		return nil
	}
	return json.RawMessage(*s)
}
