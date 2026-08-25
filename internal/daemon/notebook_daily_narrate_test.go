package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

func pinUTCSlot(t *testing.T, d *Daemon) {
	t.Helper()
	d.store.SetSetting(SettingNotebookCronTimezone, "UTC")
}

func TestEnqueueDueDailyNarratesFirstObservationAnchorsWithoutFiring(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		root := installNotebookNarrationRunner(t, d)
		pinUTCSlot(t, d)
		d.markNotebookWorkspaceActivity("ws-A")

		d.enqueueDueDailyNarrates(mustTime(t, "2026-06-14T12:00:00Z"))

		state, _ := notebook.LoadNarrateCronState(root)
		if state.ScheduledFrom == "" {
			t.Fatal("first observation should anchor the schedule")
		}
		if taskExists(t, d, notebookNarrateWorkspaceKind, "ws-A") {
			t.Fatal("first observation enqueued a narrate before the first scheduled slot")
		}
		if got := d.drainNotebookNarrateActivity(); len(got) != 1 || got[0] != "ws-A" {
			t.Fatalf("first observation drained the activity set: %v", got)
		}
	})
}

func TestEnqueueDueDailyNarratesNotDueLeavesAnchor(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		root := installNotebookNarrationRunner(t, d)
		pinUTCSlot(t, d)
		d.markNotebookWorkspaceActivity("ws-A")

		if err := notebook.SaveNarrateCronState(root, notebook.NarrateCronState{ScheduledFrom: "2026-06-14T04:00:00Z"}); err != nil {
			t.Fatalf("seed state: %v", err)
		}

		d.enqueueDueDailyNarrates(mustTime(t, "2026-06-14T05:00:00Z"))

		state, _ := notebook.LoadNarrateCronState(root)
		if state.ScheduledFrom != "2026-06-14T04:00:00Z" {
			t.Fatalf("not-due tick mutated the anchor: %q", state.ScheduledFrom)
		}
		if taskExists(t, d, notebookNarrateWorkspaceKind, "ws-A") {
			t.Fatal("not-due tick enqueued a narrate")
		}
	})
}

func TestEnqueueDueDailyNarratesDueFiresOnceAndClearsActivity(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		root := installNotebookNarrationRunner(t, d)
		pinUTCSlot(t, d)
		d.store.AddWorkspace(&protocol.Workspace{ID: "ws-A", Title: "ws-A", Directory: t.TempDir()})
		d.markNotebookWorkspaceActivity("ws-A")
		d.narrateWorkspaceExecution = blockingExecution(t)

		if err := notebook.SaveNarrateCronState(root, notebook.NarrateCronState{ScheduledFrom: "2026-06-13T03:00:00Z"}); err != nil {
			t.Fatalf("seed state: %v", err)
		}

		now := mustTime(t, "2026-06-14T12:00:00Z")
		d.enqueueDueDailyNarrates(now)

		state, _ := notebook.LoadNarrateCronState(root)
		if state.ScheduledFrom != now.UTC().Format(time.RFC3339) {
			t.Fatalf("due fire did not advance the anchor to now: %q", state.ScheduledFrom)
		}
		if !taskExists(t, d, notebookNarrateWorkspaceKind, "ws-A") {
			t.Fatal("due fire did not enqueue narrate_workspace for the active workspace")
		}
		task, err := d.jobQueue.GetByKey(notebookNarrateWorkspaceKind, "ws-A")
		if err != nil || task == nil {
			t.Fatalf("get narrate task: %v", err)
		}
		var carried narrateWorkspacePayload
		if err := task.DecodePayload(&carried); err != nil {
			t.Fatalf("decode narrate payload: %v", err)
		}
		if !carried.DailyPass {
			t.Fatalf("daily narrate job missing the daily-pass flag: %s", task.Payload)
		}

		d.store.AddWorkspace(&protocol.Workspace{ID: "ws-B", Title: "ws-B", Directory: t.TempDir()})
		d.enqueueDueDailyNarrates(mustTime(t, "2026-06-15T12:00:00Z"))
		if taskExists(t, d, notebookNarrateWorkspaceKind, "ws-B") {
			t.Fatal("a later due day with no new activity enqueued a narrate")
		}
	})
}

func TestEnqueueDueDailyNarratesGatesOnActivity(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		root := installNotebookNarrationRunner(t, d)
		pinUTCSlot(t, d)
		d.store.AddWorkspace(&protocol.Workspace{ID: "ws-A", Title: "ws-A", Directory: t.TempDir()})
		d.store.AddWorkspace(&protocol.Workspace{ID: "ws-B", Title: "ws-B", Directory: t.TempDir()})
		d.narrateWorkspaceExecution = blockingExecution(t)

		d.markNotebookWorkspaceActivity("ws-A")

		if err := notebook.SaveNarrateCronState(root, notebook.NarrateCronState{ScheduledFrom: "2026-06-13T03:00:00Z"}); err != nil {
			t.Fatalf("seed state: %v", err)
		}
		d.enqueueDueDailyNarrates(mustTime(t, "2026-06-14T12:00:00Z"))

		if !taskExists(t, d, notebookNarrateWorkspaceKind, "ws-A") {
			t.Fatal("active workspace ws-A was not narrated")
		}
		task, err := d.jobQueue.GetByKey(notebookNarrateWorkspaceKind, "ws-B")
		if err != nil {
			t.Fatalf("get ws-B narrate: %v", err)
		}
		if task != nil {
			t.Fatalf("idle workspace ws-B was unexpectedly narrated: %+v", task)
		}
	})
}

func TestEnqueueDueDailyNarratesSkipsRemovedWorkspace(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		root := installNotebookNarrationRunner(t, d)
		pinUTCSlot(t, d)
		d.narrateWorkspaceExecution = blockingExecution(t)
		d.markNotebookWorkspaceActivity("ws-gone")

		if err := notebook.SaveNarrateCronState(root, notebook.NarrateCronState{ScheduledFrom: "2026-06-13T03:00:00Z"}); err != nil {
			t.Fatalf("seed state: %v", err)
		}
		now := mustTime(t, "2026-06-14T12:00:00Z")
		d.enqueueDueDailyNarrates(now)

		state, _ := notebook.LoadNarrateCronState(root)
		if state.ScheduledFrom != now.UTC().Format(time.RFC3339) {
			t.Fatalf("anchor did not advance on a fire that skipped a removed workspace: %q", state.ScheduledFrom)
		}
		synctest.Wait()
		task, err := d.jobQueue.GetByKey(notebookNarrateWorkspaceKind, "ws-gone")
		if err != nil {
			t.Fatalf("get ws-gone narrate: %v", err)
		}
		if task != nil {
			t.Fatalf("removed workspace was narrated by the daily cron: %+v", task)
		}
	})
}

func TestHandleStopMarksWorkspaceActivity(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		setupWorkspaceContextSession(t, d, "session-1", "ws-1")
		installNotebookNarrationRunner(t, d)
		d.summarizeSessionExecution = blockingExecution(t)
		d.narrateWorkspaceExecution = blockingExecution(t)

		d.handleStop(drainingConn(t), &protocol.StopMessage{ID: "session-1"})

		got := d.drainNotebookNarrateActivity()
		if len(got) != 1 || got[0] != "ws-1" {
			t.Fatalf("stop did not mark ws-1 active: %v", got)
		}
		settleStopClassification(t)
	})
}

func TestContextWriteMarksActivityOnlyWhenChanged(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	setupWorkspaceContextSession(t, d, "session-1", "workspace-1")

	checkout, err := d.checkoutWorkspaceContext(&protocol.WorkspaceContextCheckoutMessage{SourceSessionID: "session-1"})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}

	if _, changed, err := d.updateWorkspaceContext(&protocol.WorkspaceContextUpdateMessage{SourceSessionID: "session-1"}); err != nil || changed {
		t.Fatalf("expected no-op update (changed=false), got changed=%v err=%v", changed, err)
	}
	if got := d.drainNotebookNarrateActivity(); len(got) != 0 {
		t.Fatalf("a no-op context update marked activity: %v", got)
	}

	if err := os.WriteFile(checkout.Path, []byte("# Real shared goal\n"), 0o600); err != nil {
		t.Fatalf("edit checkout: %v", err)
	}
	if _, changed, err := d.updateWorkspaceContext(&protocol.WorkspaceContextUpdateMessage{SourceSessionID: "session-1"}); err != nil || !changed {
		t.Fatalf("expected changing update (changed=true), got changed=%v err=%v", changed, err)
	}
	got := d.drainNotebookNarrateActivity()
	if len(got) != 1 || got[0] != "workspace-1" {
		t.Fatalf("a content-changing context update did not mark workspace-1 active: %v", got)
	}
}

func TestNarrateWorkspaceDailyPassUnchangedIsDone(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		setupWorkspaceContextSession(t, d, "session-1", "ws-1")
		root := installNotebookNarrationRunner(t, d)
		d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

		journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
		if err := os.MkdirAll(filepath.Dir(journal), 0o755); err != nil {
			t.Fatalf("mkdir journal: %v", err)
		}
		prior := "## ws-1 — 2026-06-15\n<!-- attn:wsnarr:ws-1 -->\n\nalready narrated today\n\nsource: workspace:ws-1\n"
		if err := os.WriteFile(journal, []byte(prior), 0o644); err != nil {
			t.Fatalf("seed prior entry: %v", err)
		}

		d.narrateWorkspaceExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			return agentdriver.HeadlessTaskResult{Diagnostics: "nothing new"}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookNarrateWorkspaceKind, jobs.EnqueueOptions{
			UniqueKey: "ws-1",
			Payload:   narrateWorkspacePayload{DailyPass: true},
		}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		requireTaskState(t, d, notebookNarrateWorkspaceKind, "ws-1", jobs.StateDone)
	})
}

func TestNarrateWorkspaceDailyPassAbsentIsDone(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		setupWorkspaceContextSession(t, d, "session-1", "ws-1")
		installNotebookNarrationRunner(t, d)
		d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

		d.narrateWorkspaceExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			return agentdriver.HeadlessTaskResult{Diagnostics: "no entry"}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookNarrateWorkspaceKind, jobs.EnqueueOptions{
			UniqueKey: "ws-1",
			Payload:   narrateWorkspacePayload{DailyPass: true},
		}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		requireTaskState(t, d, notebookNarrateWorkspaceKind, "ws-1", jobs.StateDone)
	})
}

func TestNarrateWorkspaceDailyFlagRemovalPassStillStrict(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		root := installNotebookNarrationRunner(t, d)
		d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

		journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
		if err := os.MkdirAll(filepath.Dir(journal), 0o755); err != nil {
			t.Fatalf("mkdir journal: %v", err)
		}
		prior := "## ws-removed — 2026-06-15\n<!-- attn:wsnarr:ws-removed -->\n\nprior\n\nsource: workspace:ws-removed\n"
		if err := os.WriteFile(journal, []byte(prior), 0o644); err != nil {
			t.Fatalf("seed prior entry: %v", err)
		}

		d.narrateWorkspaceExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			return agentdriver.HeadlessTaskResult{Diagnostics: "no-op"}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookNarrateWorkspaceKind, jobs.EnqueueOptions{
			UniqueKey: "ws-removed",
			Payload:   narrateWorkspacePayload{DailyPass: true},
		}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		task := requireTaskFailure(t, d, notebookNarrateWorkspaceKind, "ws-removed")
		if task.State == jobs.StateDone {
			t.Fatal("daily flag wrongly relaxed a removal pass to done")
		}
	})
}

func TestNarrateWorkspaceRoutinePassUnchangedStillFails(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		setupWorkspaceContextSession(t, d, "session-1", "ws-1")
		root := installNotebookNarrationRunner(t, d)
		d.narrationNowOverride = func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) }

		journal := filepath.Join(root, notebook.DirJournal, "2026-06-15.md")
		if err := os.MkdirAll(filepath.Dir(journal), 0o755); err != nil {
			t.Fatalf("mkdir journal: %v", err)
		}
		prior := "## ws-1 — 2026-06-15\n<!-- attn:wsnarr:ws-1 -->\n\nactive-day entry\n\nsource: workspace:ws-1\n"
		if err := os.WriteFile(journal, []byte(prior), 0o644); err != nil {
			t.Fatalf("seed prior entry: %v", err)
		}

		d.narrateWorkspaceExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			return agentdriver.HeadlessTaskResult{Diagnostics: "no-op"}, nil
		}

		if _, err := d.jobQueue.Enqueue(notebookNarrateWorkspaceKind, jobs.EnqueueOptions{UniqueKey: "ws-1"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		task := requireTaskFailure(t, d, notebookNarrateWorkspaceKind, "ws-1")
		if !strings.Contains(task.LastError, "unchanged") {
			t.Fatalf("routine pass last error = %q, want unchanged rejection", task.LastError)
		}
	})
}
