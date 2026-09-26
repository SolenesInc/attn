package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workflow"
)

type fakeWorkflowClient struct {
	mu sync.Mutex

	runs      map[string]*protocol.WorkflowRun
	callsByID map[string][]protocol.WorkflowAgentCall

	callUpserts []protocol.WorkflowAgentCall
}

func newFakeWorkflowClient() *fakeWorkflowClient {
	return &fakeWorkflowClient{
		runs:      map[string]*protocol.WorkflowRun{},
		callsByID: map[string][]protocol.WorkflowAgentCall{},
	}
}

var _ workflowClient = (*fakeWorkflowClient)(nil)

func (*fakeWorkflowClient) WorkflowRunUpsert(*protocol.WorkflowRun) (*protocol.WorkflowRun, error) {
	return nil, nil
}

func (f *fakeWorkflowClient) WorkflowCallUpsert(runID string, call *protocol.WorkflowAgentCall) (*protocol.WorkflowRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored := *call
	if stored.RunID == "" {
		stored.RunID = runID
	}
	f.callUpserts = append(f.callUpserts, stored)

	calls := f.callsByID[runID]
	replaced := false
	for i := range calls {
		if calls[i].Ordinal == stored.Ordinal {
			calls[i] = stored
			replaced = true
			break
		}
	}
	if !replaced {
		calls = append(calls, stored)
	}
	f.callsByID[runID] = calls
	return f.hydrateLocked(runID), nil
}

func (f *fakeWorkflowClient) WorkflowRunGet(runID string) (*protocol.WorkflowRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hydrateLocked(runID), nil
}

func (*fakeWorkflowClient) WorkflowRunList(string) ([]protocol.WorkflowRun, error) {
	return nil, nil
}

func (*fakeWorkflowClient) WorkflowRunCancel(string) (*protocol.WorkflowRun, error) {
	return nil, nil
}

func (f *fakeWorkflowClient) hydrateLocked(runID string) *protocol.WorkflowRun {
	run, ok := f.runs[runID]
	if !ok {
		return nil
	}
	copied := *run
	calls := f.callsByID[runID]
	if len(calls) > 0 {
		copied.AgentCalls = append([]protocol.WorkflowAgentCall(nil), calls...)
	}
	return &copied
}

func (f *fakeWorkflowClient) seedRun(run protocol.WorkflowRun) {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := run.AgentCalls
	run.AgentCalls = nil
	saved := run
	f.runs[run.RunID] = &saved
	if len(calls) > 0 {
		f.callsByID[run.RunID] = append([]protocol.WorkflowAgentCall(nil), calls...)
	}
}

func (f *fakeWorkflowClient) callUpsertCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.callUpserts)
}

func TestWorkflowIPCJournalProxiesAndMirrors(t *testing.T) {
	fake := newFakeWorkflowClient()
	fake.seedRun(protocol.WorkflowRun{RunID: "wf-1", Status: protocol.WorkflowRunStatusRunning})

	j := NewIPCJournal(fake, "wf-1")

	entry := workflow.JournalEntry{
		Ordinal:    "ord-1",
		PromptHash: "ph",
		SchemaHash: "none",
		Result:     json.RawMessage(`"hi"`),
		Status:     "ok",
	}
	if err := j.Append(entry); err != nil {
		t.Fatalf("append: %v", err)
	}

	if got, ok := j.Lookup("ord-1"); !ok || got.PromptHash != "ph" {
		t.Fatalf("lookup miss after append: %+v ok=%v", got, ok)
	}
	if len(j.Entries()) != 1 {
		t.Fatalf("entries = %d, want 1", len(j.Entries()))
	}

	if fake.callUpsertCount() != 1 {
		t.Fatalf("call upserts = %d, want 1", fake.callUpsertCount())
	}
	if got := fake.callUpserts[0]; got.Ordinal != "ord-1" || got.Status != protocol.WorkflowAgentCallStatusOk {
		t.Fatalf("proxied call = %+v", got)
	}

	j.Upsert(workflow.JournalEntry{Ordinal: "ord-1", PromptHash: "ph2", SchemaHash: "none", Status: "ok"})
	if got, _ := j.Lookup("ord-1"); got.PromptHash != "ph2" {
		t.Fatalf("upsert did not overwrite mirror: %+v", got)
	}
	if fake.callUpsertCount() != 2 {
		t.Fatalf("call upserts after upsert = %d, want 2", fake.callUpsertCount())
	}
}

func TestWorkflowIPCJournalSeedsFromDaemon(t *testing.T) {
	fake := newFakeWorkflowClient()
	fake.seedRun(protocol.WorkflowRun{
		RunID:  "wf-1",
		Status: protocol.WorkflowRunStatusRunning,
		AgentCalls: []protocol.WorkflowAgentCall{
			{
				RunID:      "wf-1",
				Ordinal:    "ord-seed",
				PromptHash: protocol.Ptr("seed-ph"),
				SchemaHash: protocol.Ptr("none"),
				ResultJson: protocol.Ptr(`"seeded"`),
				Status:     protocol.WorkflowAgentCallStatusOk,
			},
		},
	})

	j := NewIPCJournal(fake, "wf-1")

	got, ok := j.Lookup("ord-seed")
	if !ok {
		t.Fatal("seeded entry not found in mirror")
	}
	if got.PromptHash != "seed-ph" || string(got.Result) != `"seeded"` {
		t.Fatalf("seeded entry = %+v", got)
	}
	if fake.callUpsertCount() != 0 {
		t.Fatalf("seeding should not proxy; got %d call upserts", fake.callUpsertCount())
	}
}

func TestBuildWorkflowShowOutput(t *testing.T) {
	run := &protocol.WorkflowRun{
		RunID:      "wf-9",
		Status:     protocol.WorkflowRunStatusRunning,
		Phase:      protocol.Ptr("review"),
		ScriptPath: "pipeline.js",
		Resumable:  true,
		CreatedAt:  "2026-06-16T22:00:00Z",
		UpdatedAt:  "2026-06-16T22:05:00Z",
		AgentCalls: []protocol.WorkflowAgentCall{
			{
				Ordinal: "ph1/cs@p.js:1#0", Status: protocol.WorkflowAgentCallStatusOk,
				Label: protocol.Ptr("plan"), Phase: protocol.Ptr("plan"),
				ResolvedModel: protocol.Ptr("gpt-5-codex"),
				StartedAt:     protocol.Ptr("2026-06-16T22:00:00Z"),
				CompletedAt:   protocol.Ptr("2026-06-16T22:00:41Z"),
			},
			{
				Ordinal: "ph2/cs@p.js:9#0", Status: protocol.WorkflowAgentCallStatusRunning,
				Label: protocol.Ptr("review changes"), Phase: protocol.Ptr("review"),
				ResolvedModel: protocol.Ptr("gpt-5-codex"),
				StartedAt:     protocol.Ptr("2026-06-16T22:04:00Z"),
			},
		},
	}

	out := buildWorkflowShowOutput(run)
	if out.Status != "running" || out.Phase != "review" || out.Script != "pipeline.js" {
		t.Fatalf("header = %+v", out)
	}
	if out.Progress.CallsTotal != 2 || out.Progress.CallsDone != 1 || out.Progress.CallsRunning != 1 {
		t.Fatalf("progress = %+v, want total=2 done=1 running=1", out.Progress)
	}
	if !strings.Contains(out.Progress.Summary, "running") || !strings.Contains(out.Progress.Summary, "review") {
		t.Fatalf("summary = %q, want it to mention running + phase", out.Progress.Summary)
	}
	if len(out.Calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(out.Calls))
	}
	done := out.Calls[0]
	if done.Label != "plan" || done.Model != "gpt-5-codex" {
		t.Fatalf("done call lost display fields: %+v", done)
	}
	if done.ElapsedSeconds == nil || *done.ElapsedSeconds != 41 {
		t.Fatalf("done elapsed = %v, want 41", done.ElapsedSeconds)
	}
	running := out.Calls[1]
	if running.Status != "running" || running.Label != "review changes" || running.Phase != "review" {
		t.Fatalf("running call = %+v", running)
	}
	if running.ElapsedSeconds == nil {
		t.Fatalf("running elapsed should be non-nil (started->now)")
	}
}

func TestCountWorkflowCallsRunning(t *testing.T) {
	calls := []protocol.WorkflowAgentCall{
		{Status: protocol.WorkflowAgentCallStatusOk},
		{Status: protocol.WorkflowAgentCallStatusErrored},
		{Status: protocol.WorkflowAgentCallStatusSkipped},
		{Status: protocol.WorkflowAgentCallStatusRunning},
		{Status: protocol.WorkflowAgentCallStatusRunning},
	}
	total, done, running := countWorkflowCalls(calls)
	if total != 5 || done != 3 || running != 2 {
		t.Fatalf("counts = (%d,%d,%d), want (5,3,2)", total, done, running)
	}
}
