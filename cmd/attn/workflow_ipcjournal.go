package main

import (
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workflow"
)

type workflowClient interface {
	WorkflowRunUpsert(run *protocol.WorkflowRun) (*protocol.WorkflowRun, error)
	WorkflowCallUpsert(runID string, call *protocol.WorkflowAgentCall) (*protocol.WorkflowRun, error)
	WorkflowRunGet(runID string) (*protocol.WorkflowRun, error)
	WorkflowRunList(sessionID string) ([]protocol.WorkflowRun, error)
	WorkflowRunCancel(runID string) (*protocol.WorkflowRun, error)
}

type ipcJournal struct {
	client  workflowClient
	runID   string
	mirror  *workflow.MemJournal
	lastErr error
}

var _ workflow.Journal = (*ipcJournal)(nil)

func NewIPCJournal(c workflowClient, runID string) *ipcJournal {
	j := &ipcJournal{
		client: c,
		runID:  runID,
		mirror: workflow.NewMemJournal(),
	}
	run, err := c.WorkflowRunGet(runID)
	if err != nil {
		j.lastErr = err
		return j
	}
	if run != nil {
		for i := range run.AgentCalls {
			j.mirror.Upsert(entryFromCall(run.AgentCalls[i]))
		}
	}
	return j
}

func (j *ipcJournal) Lookup(ordinal string) (workflow.JournalEntry, bool) {
	return j.mirror.Lookup(ordinal)
}

func (j *ipcJournal) Append(e workflow.JournalEntry) error {
	if err := j.mirror.Append(e); err != nil {
		return err
	}
	j.proxy(e)
	return nil
}

// Upsert overwrites any stale entry at the same ordinal. The Journal interface gives
// it no error return, so a proxy failure is captured in lastErr rather than dropped.
func (j *ipcJournal) Upsert(e workflow.JournalEntry) {
	j.mirror.Upsert(e)
	j.proxy(e)
}

func (j *ipcJournal) Entries() []workflow.JournalEntry {
	return j.mirror.Entries()
}

func (j *ipcJournal) Err() error {
	return j.lastErr
}

func (j *ipcJournal) proxy(e workflow.JournalEntry) {
	call := callFromEntry(j.runID, e)
	if _, err := j.client.WorkflowCallUpsert(j.runID, &call); err != nil {
		j.lastErr = err
	}
}

func callFromEntry(runID string, e workflow.JournalEntry) protocol.WorkflowAgentCall {
	return protocol.WorkflowAgentCall{
		RunID:         runID,
		Ordinal:       e.Ordinal,
		Label:         ptrIfNonEmpty(e.Label),
		Phase:         ptrIfNonEmpty(e.Phase),
		ResolvedModel: ptrIfNonEmpty(e.Model),
		PromptHash:    ptrIfNonEmpty(e.PromptHash),
		SchemaHash:    ptrIfNonEmpty(e.SchemaHash),
		ResultJson:    rawResultToPtr(e.Result),
		Status:        protocol.WorkflowAgentCallStatus(callStatusOrOk(e.Status)),
		Error:         ptrIfNonEmpty(e.Err),
		StartedAt:     ptrIfNonEmpty(e.StartedAt),
		CompletedAt:   ptrIfNonEmpty(e.CompletedAt),
	}
}

// entryFromCall round-trips losslessly for the six JournalEntry fields, the only
// correctness requirement: IsCacheHit needs Ordinal+PromptHash+SchemaHash.
func entryFromCall(call protocol.WorkflowAgentCall) workflow.JournalEntry {
	return workflow.JournalEntry{
		Ordinal:    call.Ordinal,
		PromptHash: protocol.Deref(call.PromptHash),
		SchemaHash: protocol.Deref(call.SchemaHash),
		Result:     ptrToRawResult(call.ResultJson),
		Status:     string(call.Status),
		Err:        protocol.Deref(call.Error),
	}
}

// The engine writes "ok" | "skipped" | "errored"; an empty status becomes "ok".
func callStatusOrOk(status string) string {
	switch status {
	case string(protocol.WorkflowAgentCallStatusOk),
		string(protocol.WorkflowAgentCallStatusErrored),
		string(protocol.WorkflowAgentCallStatusSkipped),
		string(protocol.WorkflowAgentCallStatusRunning):
		return status
	default:
		return string(protocol.WorkflowAgentCallStatusOk)
	}
}
