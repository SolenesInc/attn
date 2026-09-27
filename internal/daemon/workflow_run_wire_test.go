package daemon_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestWorkflowRunsSurviveARestartNewestFirstEvenWithinOneSecond(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	second := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	for _, run := range []struct {
		id      string
		session string
		offset  time.Duration
	}{
		{"d-first", "ship", 0},
		{"c-second", "audit", 123400 * time.Microsecond},
		{"b-third", "ship", 123450 * time.Microsecond},
		{"a-fourth", "audit", 500 * time.Millisecond},
	} {
		at := second.Add(run.offset).Format(time.RFC3339Nano)
		if _, err := cli.WorkflowRunUpsert(&protocol.WorkflowRun{
			RunID: run.id, ScriptPath: "ship.ts", ScriptHash: "h", Status: protocol.WorkflowRunStatusCompleted,
			ArgsJson: protocol.Ptr(`{"target":"main"}`), SessionID: protocol.Ptr(run.session), Phase: protocol.Ptr("plan"),
			Resumable: true, CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatalf("upsert %s: %v", run.id, err)
		}
	}

	for _, call := range []protocol.WorkflowAgentCall{
		{RunID: "b-third", Ordinal: "0", Label: protocol.Ptr("review"), Status: protocol.WorkflowAgentCallStatusRunning},
		{RunID: "b-third", Ordinal: "0", Label: protocol.Ptr("review"), Status: protocol.WorkflowAgentCallStatusOk, CompletedAt: protocol.Ptr("2026-08-06T10:05:00Z")},
		{RunID: "b-third", Ordinal: "1", Label: protocol.Ptr("summarize"), Status: protocol.WorkflowAgentCallStatusRunning},
	} {
		if _, err := cli.WorkflowCallUpsert("b-third", &call); err != nil {
			t.Fatalf("record call %s: %v", call.Ordinal, err)
		}
	}

	w.restart()
	cli = w.Client()

	third, err := cli.WorkflowRunGet("b-third")
	if err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(third.ArgsJson) != `{"target":"main"}` || protocol.Deref(third.SessionID) != "ship" || protocol.Deref(third.Phase) != "plan" || !third.Resumable {
		t.Errorf("b-third after restart = %+v, want its args, session, phase and resumable flag", third)
	}
	var calls []string
	for _, call := range third.AgentCalls {
		calls = append(calls, call.Ordinal+":"+protocol.Deref(call.Label)+":"+string(call.Status)+":"+protocol.Deref(call.CompletedAt))
	}
	if want := []string{"0:review:ok:2026-08-06T10:05:00Z", "1:summarize:running:"}; !slices.Equal(calls, want) {
		t.Errorf("b-third's calls after restart = %v, want the re-recorded call updated in place and the new one after it %v", calls, want)
	}
	for session, want := range map[string][]string{
		"":     {"a-fourth", "b-third", "c-second", "d-first"},
		"ship": {"b-third", "d-first"},
	} {
		runs, err := cli.WorkflowRunList(session)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(runs))
		for _, run := range runs {
			ids = append(ids, run.RunID)
		}
		if !slices.Equal(ids, want) {
			t.Errorf("workflow_run_list(%q) = %v, want newest first %v", session, ids, want)
		}
	}
}

func TestWorkflowRunsStartOnlyWhileWorkflowsAreEnabled(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()

	if _, err := cli.WorkflowRunUpsert(runningWorkflowRun("refused")); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("starting a run while workflows are off = %v, want it refused as disabled", err)
	}
	if refused, err := cli.WorkflowRunGet("refused"); err != nil || refused != nil {
		t.Errorf("the refused run reads back as %+v (%v), want nothing stored", refused, err)
	}
	finished := runningWorkflowRun("finished")
	finished.Status = protocol.WorkflowRunStatusCompleted
	if _, err := cli.WorkflowRunUpsert(finished); err != nil {
		t.Errorf("recording a finished run while workflows are off: %v", err)
	}

	setSetting(t, app, "workflows_enabled", "true")
	if started, err := cli.WorkflowRunUpsert(runningWorkflowRun("started")); err != nil || started.Status != protocol.WorkflowRunStatusRunning {
		t.Errorf("starting a run once workflows are on = %+v, %v", started, err)
	}
}

func TestCancellingAWorkflowRunRecordsItCanceledForItsEngine(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "workflows_enabled", "true")
	if _, err := cli.WorkflowRunUpsert(runningWorkflowRun("ship")); err != nil {
		t.Fatal(err)
	}

	result := testworld.Request(app, protocol.WorkflowRunCancelMessage{Cmd: protocol.CmdWorkflowRunCancel, RunID: "ship"},
		protocol.EventWorkflowActionResult, func(r protocol.WorkflowActionResultMessage) bool { return r.Action == "cancel" })
	if !result.Success || result.Run == nil || result.Run.Status != protocol.WorkflowRunStatusCanceled || protocol.Deref(result.Run.CompletedAt) == "" {
		t.Fatalf("cancel from the app = %+v, want the run canceled with a completion time", result)
	}
	testworld.Await(app, protocol.EventWorkflowRunUpdated, func(m protocol.WorkflowRunUpdatedMessage) bool {
		return m.Run.RunID == "ship" && m.Run.Status == protocol.WorkflowRunStatusCanceled
	})
	if seen, err := cli.WorkflowRunGet("ship"); err != nil || seen.Status != protocol.WorkflowRunStatusCanceled {
		t.Errorf("the engine reads the run back as %+v (%v), want canceled", seen, err)
	}

	if unknown, err := cli.WorkflowRunCancel("never-ran"); err != nil || unknown != nil {
		t.Errorf("cancelling an unknown run = %+v, %v; want a harmless no-op", unknown, err)
	}
}

func TestWorkflowRunUpdatesReachTheAppCoalescedPerRun(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setSetting(t, app, "workflows_enabled", "true")
		for _, id := range []string{"busy", "quiet"} {
			if _, err := cli.WorkflowRunUpsert(runningWorkflowRun(id)); err != nil {
				t.Fatal(err)
			}
		}
		for ordinal := range 10 {
			call := protocol.WorkflowAgentCall{RunID: "busy", Ordinal: string(rune('a' + ordinal)), Status: protocol.WorkflowAgentCallStatusRunning}
			if _, err := cli.WorkflowCallUpsert("busy", &call); err != nil {
				t.Fatal(err)
			}
		}

		w.advance(time.Second)

		for id, calls := range map[string]int{"busy": 10, "quiet": 0} {
			update := testworld.Await(app, protocol.EventWorkflowRunUpdated, func(m protocol.WorkflowRunUpdatedMessage) bool { return m.Run.RunID == id })
			if len(update.Run.AgentCalls) != calls {
				t.Errorf("the update for %s carries %d calls, want the whole run with %d", id, len(update.Run.AgentCalls), calls)
			}
		}
		if updates := automationEventCount(app, protocol.EventWorkflowRunUpdated); updates != 2 {
			t.Errorf("the app got %d workflow_run_updated events, want one per run", updates)
		}
	})
}

func runningWorkflowRun(id string) *protocol.WorkflowRun {
	return &protocol.WorkflowRun{
		RunID: id, ScriptPath: "ship.ts", ScriptHash: "h", Status: protocol.WorkflowRunStatusRunning,
		CreatedAt: "2026-08-06T10:00:00Z", UpdatedAt: "2026-08-06T10:00:00Z",
	}
}
