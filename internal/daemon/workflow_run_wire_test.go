package daemon_test

import (
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestWorkflowRunsListNewestFirstEvenWithinOneSecond(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	second := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	for _, run := range []struct {
		id     string
		offset time.Duration
	}{
		{"d-first", 0},
		{"c-second", 123400 * time.Microsecond},
		{"b-third", 123450 * time.Microsecond},
		{"a-fourth", 500 * time.Millisecond},
	} {
		at := second.Add(run.offset).Format(time.RFC3339Nano)
		if _, err := cli.WorkflowRunUpsert(&protocol.WorkflowRun{
			RunID: run.id, ScriptPath: "ship.ts", ScriptHash: "h", Status: protocol.WorkflowRunStatusCompleted,
			CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatalf("upsert %s: %v", run.id, err)
		}
	}

	runs, err := cli.WorkflowRunList("")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.RunID)
	}
	if want := []string{"a-fourth", "b-third", "c-second", "d-first"}; !slices.Equal(ids, want) {
		t.Errorf("workflow_run_list = %v, want newest first %v", ids, want)
	}
}
